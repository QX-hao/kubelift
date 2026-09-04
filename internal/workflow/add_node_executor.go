package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/install"
	"github.com/QX-hao/kubelift/internal/kubeadm"
	"github.com/QX-hao/kubelift/internal/remote"
)

const defaultRemoteJoinConfig = "/etc/kubernetes/kubelift-join.yaml"

type nodeClient interface {
	install.CommandRunner
	distribute.FileTransport
}

// AddNodeExecutor 描述由 Master0 准备并加入一个 Worker 所需的依赖。
type AddNodeExecutor struct {
	MasterRunner     install.CommandRunner
	Remote           nodeClient
	RemoteRoot       string
	RemoteJoinConfig string
	AdminKubeconfig  string
	StateRoot        string
}

// AddNodeResult 汇总 Worker 加入阶段完成的工作。
type AddNodeResult struct {
	NodeName        string
	PayloadCount    int
	Preparation     install.Report
	Images          install.ImageReport
	KubeadmOutput   string
	Phase           AddPhase
	AlreadyComplete bool
}

func (e AddNodeExecutor) Execute(ctx context.Context, configuration config.Config, target Target, options JoinOptions) (AddNodeResult, error) {
	if err := configuration.Validate(); err != nil {
		return AddNodeResult{}, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if target.Role != RoleNode {
		return AddNodeResult{}, fmt.Errorf("add node executor requires a worker target")
	}
	if err := validateTarget(target); err != nil {
		return AddNodeResult{}, err
	}
	if target.SSHUser != "root" {
		return AddNodeResult{}, fmt.Errorf("real node installation currently requires SSH user root")
	}
	if e.MasterRunner == nil || e.Remote == nil {
		return AddNodeResult{}, fmt.Errorf("add node executor dependencies are incomplete")
	}
	if !path.IsAbs(e.RemoteRoot) || path.Clean(e.RemoteRoot) == "/" || !path.IsAbs(e.RemoteJoinConfig) || !filepath.IsAbs(e.StateRoot) {
		return AddNodeResult{}, fmt.Errorf("remote managed paths must be absolute non-root paths")
	}

	nodeName := target.Name
	if nodeName == "" {
		result, err := e.Remote.Run(ctx, "hostname")
		if err != nil {
			return AddNodeResult{}, commandError("read target hostname", result, err)
		}
		nodeName = strings.ToLower(strings.TrimSpace(result.Stdout))
		candidate := target
		candidate.Name = nodeName
		if err := validateTarget(candidate); err != nil {
			return AddNodeResult{}, fmt.Errorf("remote hostname cannot be used as the Kubernetes node name: %w", err)
		}
	}
	state, statePath, err := prepareAddState(configuration, target, nodeName, e.StateRoot, options.Resume)
	if err != nil {
		return AddNodeResult{}, err
	}
	joined, err := remoteNodeJoined(ctx, e.Remote)
	if err != nil {
		return AddNodeResult{}, err
	}
	if state.Phase == AddPhaseComplete {
		if !joined {
			return AddNodeResult{}, fmt.Errorf("add state is complete but target kubelet.conf is missing")
		}
		return AddNodeResult{NodeName: nodeName, Phase: AddPhaseComplete, AlreadyComplete: true}, nil
	}
	if state.reached(AddPhaseJoined) && !joined {
		return AddNodeResult{}, fmt.Errorf("add state is %q but target kubelet.conf is missing", state.Phase)
	}
	if !state.reached(AddPhaseJoinStarting) && joined {
		return AddNodeResult{}, fmt.Errorf("target already appears joined but add state has only reached %q", state.Phase)
	}
	if state.Phase == AddPhaseJoinStarting && !joined {
		return AddNodeResult{}, fmt.Errorf("kubeadm join may be incomplete; inspect the target before retrying and do not run kubeadm reset automatically")
	}
	if !state.reached(AddPhaseJoinStarting) {
		if err := ensureTargetNotInCluster(ctx, e.MasterRunner, e.AdminKubeconfig, nodeName, target.Address); err != nil {
			return AddNodeResult{}, err
		}
	}

	var staged *distribute.Report
	if !state.reached(AddPhaseImagesImported) {
		staged, err = distribute.Push(ctx, e.Remote, configuration.Spec.Offline.Bundle, e.RemoteRoot)
		if err != nil {
			return AddNodeResult{}, fmt.Errorf("stage worker bundle: %w", err)
		}
		if err := bundle.ValidateClusterProfile(staged.Manifest, false); err != nil {
			return AddNodeResult{}, fmt.Errorf("validate worker bundle profile: %w", err)
		}
		if !state.reached(AddPhaseStaged) {
			if err := saveAddState(statePath, state, AddPhaseStaged); err != nil {
				return AddNodeResult{}, err
			}
			state.Phase = AddPhaseStaged
		}
	}
	var preparation install.Report
	if !state.reached(AddPhasePrepared) {
		preparation, err = install.PrepareNode(ctx, e.Remote, staged.RemoteRoot, staged.Manifest)
		if err != nil {
			return AddNodeResult{}, err
		}
		if err := saveAddState(statePath, state, AddPhasePrepared); err != nil {
			return AddNodeResult{}, err
		}
		state.Phase = AddPhasePrepared
	}
	var images install.ImageReport
	if !state.reached(AddPhaseImagesImported) {
		images, err = install.ImportImages(ctx, e.Remote, staged.RemoteRoot, staged.Manifest, install.ImageOptions{})
		if err != nil {
			return AddNodeResult{}, err
		}
		if err := saveAddState(statePath, state, AddPhaseImagesImported); err != nil {
			return AddNodeResult{}, err
		}
		state.Phase = AddPhaseImagesImported
	}

	var joinResult remote.CommandResult
	if !state.reached(AddPhaseJoined) {
		if state.Phase == AddPhaseJoinStarting && joined {
			state.Phase = AddPhaseJoined
		} else {
			tokenCommand := "/usr/bin/kubeadm token create --ttl 2h --print-join-command --kubeconfig " + quoteShell(e.AdminKubeconfig)
			tokenResult, err := e.MasterRunner.Run(ctx, tokenCommand)
			if err != nil {
				return AddNodeResult{}, commandError("create kubeadm bootstrap token", tokenResult, err)
			}
			credentials, err := kubeadm.ParseJoinCommand(tokenResult.Stdout)
			if err != nil {
				return AddNodeResult{}, err
			}
			expectedEndpoint := configuration.Spec.ControlPlane.Endpoint
			if expectedEndpoint == "" {
				expectedEndpoint = configuration.Spec.ControlPlane.AdvertiseAddress + ":6443"
			}
			if credentials.APIServerEndpoint != expectedEndpoint {
				return AddNodeResult{}, fmt.Errorf("kubeadm join endpoint %q does not match configured endpoint %q", credentials.APIServerEndpoint, expectedEndpoint)
			}
			joinConfig, err := kubeadm.GenerateWorkerJoinConfig(credentials, nodeName)
			if err != nil {
				return AddNodeResult{}, err
			}
			if err := uploadGeneratedFile(ctx, e.Remote, e.RemoteJoinConfig, joinConfig); err != nil {
				return AddNodeResult{}, err
			}
			if err := saveAddState(statePath, state, AddPhaseJoinStarting); err != nil {
				return AddNodeResult{}, err
			}
			state.Phase = AddPhaseJoinStarting
			joinConfigPath := quoteShell(e.RemoteJoinConfig)
			joinResult, err = e.Remote.Run(ctx, "trap \"rm -f -- "+joinConfigPath+"\" EXIT; /usr/bin/kubeadm join --ignore-preflight-errors=FileExisting-conntrack --config "+joinConfigPath)
			if err != nil {
				return AddNodeResult{}, commandError("join worker node", joinResult, err)
			}
			joined, err = remoteNodeJoined(ctx, e.Remote)
			if err != nil {
				return AddNodeResult{}, err
			}
			if !joined {
				return AddNodeResult{}, fmt.Errorf("kubeadm join succeeded but target kubelet.conf was not created")
			}
		}
		if err := saveAddState(statePath, state, AddPhaseJoined); err != nil {
			return AddNodeResult{}, err
		}
		state.Phase = AddPhaseJoined
	}
	if !state.reached(AddPhaseReady) {
		waitCommand := "/usr/bin/kubectl --kubeconfig " + quoteShell(e.AdminKubeconfig) +
			" wait --for=condition=Ready node/" + quoteShell(nodeName) + " --timeout=10m"
		if result, err := e.MasterRunner.Run(ctx, waitCommand); err != nil {
			return AddNodeResult{}, commandError("wait for worker node readiness", result, err)
		}
		if err := saveAddState(statePath, state, AddPhaseReady); err != nil {
			return AddNodeResult{}, err
		}
		state.Phase = AddPhaseReady
	}
	if err := saveAddState(statePath, state, AddPhaseComplete); err != nil {
		return AddNodeResult{}, err
	}
	payloadCount := 0
	if staged != nil {
		payloadCount = len(staged.Files)
	}
	return AddNodeResult{
		NodeName: nodeName, PayloadCount: payloadCount, Preparation: preparation,
		Images: images, KubeadmOutput: strings.TrimSpace(joinResult.Stdout), Phase: AddPhaseComplete,
	}, nil
}

func remoteNodeJoined(ctx context.Context, runner install.CommandRunner) (bool, error) {
	result, err := runner.Run(ctx, "if [ -s /etc/kubernetes/kubelet.conf ]; then printf joined; else printf absent; fi")
	if err != nil {
		return false, commandError("inspect target kubelet configuration", result, err)
	}
	switch strings.TrimSpace(result.Stdout) {
	case "joined":
		return true, nil
	case "absent":
		return false, nil
	default:
		return false, fmt.Errorf("inspect target kubelet configuration returned unexpected output")
	}
}

func ensureTargetNotInCluster(ctx context.Context, runner install.CommandRunner, kubeconfigPath, nodeName, address string) error {
	command := "/usr/bin/kubectl --kubeconfig " + quoteShell(kubeconfigPath) + " get nodes -o json"
	result, err := runner.Run(ctx, command)
	if err != nil {
		return commandError("list existing Kubernetes nodes", result, err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &list); err != nil {
		return fmt.Errorf("decode existing Kubernetes nodes: %w", err)
	}
	for _, node := range list.Items {
		if node.Metadata.Name == nodeName {
			return fmt.Errorf("Kubernetes node name %q already exists", nodeName)
		}
		for _, existingAddress := range node.Status.Addresses {
			if existingAddress.Type == "InternalIP" && existingAddress.Address == address {
				return fmt.Errorf("target address %q is already used by Kubernetes node %q", address, node.Metadata.Name)
			}
		}
	}
	return nil
}

func uploadGeneratedFile(ctx context.Context, transport distribute.FileTransport, destination string, contents []byte) error {
	directory, err := os.MkdirTemp("", "kubelift-generated-")
	if err != nil {
		return fmt.Errorf("create temporary generated-file directory: %w", err)
	}
	defer os.RemoveAll(directory)
	source := filepath.Join(directory, filepath.Base(destination))
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		return fmt.Errorf("write generated file: %w", err)
	}
	if err := transport.UploadFile(ctx, source, destination); err != nil {
		return fmt.Errorf("upload generated file %q: %w", destination, err)
	}
	hash := sha256.Sum256(contents)
	if err := transport.VerifySHA256(ctx, destination, fmt.Sprintf("%x", hash)); err != nil {
		return fmt.Errorf("verify generated file %q: %w", destination, err)
	}
	return nil
}

func commandError(action string, result remote.CommandResult, err error) error {
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		return fmt.Errorf("%s: %w; stderr: %s", action, err, stderr)
	}
	return fmt.Errorf("%s: %w", action, err)
}
