package workflow

import (
	"context"
	"fmt"
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

// AddMasterExecutor 描述由 Master0 准备并加入一个控制平面节点所需的依赖。
type AddMasterExecutor struct {
	MasterRunner     install.CommandRunner
	Remote           nodeClient
	RemoteRoot       string
	RemoteJoinConfig string
	AdminKubeconfig  string
	StateRoot        string
}

// AddMasterResult 汇总控制平面节点加入阶段完成的工作。
type AddMasterResult struct {
	NodeName        string
	PayloadCount    int
	Preparation     install.Report
	Images          install.ImageReport
	KubeadmOutput   string
	Phase           AddPhase
	AlreadyComplete bool
}

func (e AddMasterExecutor) Execute(ctx context.Context, configuration config.Config, target Target, options JoinOptions) (AddMasterResult, error) {
	if err := configuration.Validate(); err != nil {
		return AddMasterResult{}, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if configuration.Spec.ControlPlane.Endpoint == "" {
		return AddMasterResult{}, fmt.Errorf("spec.controlPlane.endpoint is required before adding a master")
	}
	if target.Role != RoleMaster {
		return AddMasterResult{}, fmt.Errorf("add master executor requires a control-plane target")
	}
	if err := validateTarget(target); err != nil {
		return AddMasterResult{}, err
	}
	if target.SSHUser != "root" {
		return AddMasterResult{}, fmt.Errorf("real control-plane installation currently requires SSH user root")
	}
	if e.MasterRunner == nil || e.Remote == nil {
		return AddMasterResult{}, fmt.Errorf("add master executor dependencies are incomplete")
	}
	if !path.IsAbs(e.RemoteRoot) || path.Clean(e.RemoteRoot) == "/" || !path.IsAbs(e.RemoteJoinConfig) || !filepath.IsAbs(e.StateRoot) {
		return AddMasterResult{}, fmt.Errorf("remote managed paths must be absolute non-root paths")
	}

	nodeName := target.Name
	if nodeName == "" {
		result, err := e.Remote.Run(ctx, "hostname")
		if err != nil {
			return AddMasterResult{}, commandError("read target hostname", result, err)
		}
		nodeName = strings.ToLower(strings.TrimSpace(result.Stdout))
		candidate := target
		candidate.Name = nodeName
		if err := validateTarget(candidate); err != nil {
			return AddMasterResult{}, fmt.Errorf("remote hostname cannot be used as the Kubernetes node name: %w", err)
		}
	}
	state, statePath, err := prepareAddState(configuration, target, nodeName, e.StateRoot, options.Resume)
	if err != nil {
		return AddMasterResult{}, err
	}
	joined, err := remoteNodeJoined(ctx, e.Remote)
	if err != nil {
		return AddMasterResult{}, err
	}
	if state.Phase == AddPhaseComplete {
		if !joined {
			return AddMasterResult{}, fmt.Errorf("add state is complete but target kubelet.conf is missing")
		}
		return AddMasterResult{NodeName: nodeName, Phase: AddPhaseComplete, AlreadyComplete: true}, nil
	}
	if state.reached(AddPhaseJoined) && !joined {
		return AddMasterResult{}, fmt.Errorf("add state is %q but target kubelet.conf is missing", state.Phase)
	}
	if !state.reached(AddPhaseJoinStarting) && joined {
		return AddMasterResult{}, fmt.Errorf("target already appears joined but add state has only reached %q", state.Phase)
	}
	if state.Phase == AddPhaseJoinStarting && !joined {
		return AddMasterResult{}, fmt.Errorf("kubeadm join may be incomplete; inspect the target before retrying and do not run kubeadm reset automatically")
	}
	if !state.reached(AddPhaseJoinStarting) {
		if err := ensureTargetNotInCluster(ctx, e.MasterRunner, e.AdminKubeconfig, nodeName, target.Address); err != nil {
			return AddMasterResult{}, err
		}
	}

	var staged *distribute.Report
	if !state.reached(AddPhaseImagesImported) {
		staged, err = distribute.Push(ctx, e.Remote, configuration.Spec.Offline.Bundle, e.RemoteRoot)
		if err != nil {
			return AddMasterResult{}, fmt.Errorf("stage control-plane bundle: %w", err)
		}
		if err := bundle.ValidateClusterProfile(staged.Manifest, false); err != nil {
			return AddMasterResult{}, fmt.Errorf("validate control-plane bundle profile: %w", err)
		}
		if !state.reached(AddPhaseStaged) {
			if err := saveAddState(statePath, state, AddPhaseStaged); err != nil {
				return AddMasterResult{}, err
			}
			state.Phase = AddPhaseStaged
		}
	}
	var preparation install.Report
	if !state.reached(AddPhasePrepared) {
		preparation, err = install.PrepareNode(ctx, e.Remote, staged.RemoteRoot, staged.Manifest)
		if err != nil {
			return AddMasterResult{}, err
		}
		if err := saveAddState(statePath, state, AddPhasePrepared); err != nil {
			return AddMasterResult{}, err
		}
		state.Phase = AddPhasePrepared
	}
	var images install.ImageReport
	if !state.reached(AddPhaseImagesImported) {
		images, err = install.ImportImages(ctx, e.Remote, staged.RemoteRoot, staged.Manifest, install.ImageOptions{})
		if err != nil {
			return AddMasterResult{}, err
		}
		if err := saveAddState(statePath, state, AddPhaseImagesImported); err != nil {
			return AddMasterResult{}, err
		}
		state.Phase = AddPhaseImagesImported
	}

	var joinResult remote.CommandResult
	if !state.reached(AddPhaseJoined) {
		if state.Phase == AddPhaseJoinStarting && joined {
			state.Phase = AddPhaseJoined
		} else {
			uploadCommand := "/usr/bin/kubeadm init phase upload-certs --upload-certs --kubeconfig " + quoteShell(e.AdminKubeconfig)
			uploadResult, err := e.MasterRunner.Run(ctx, uploadCommand)
			if err != nil {
				return AddMasterResult{}, commandError("upload control-plane certificates", uploadResult, err)
			}
			certificateKey, err := kubeadm.ParseCertificateKey(uploadResult.Stdout)
			if err != nil {
				return AddMasterResult{}, err
			}
			tokenCommand := "/usr/bin/kubeadm token create --ttl 2h --print-join-command --kubeconfig " + quoteShell(e.AdminKubeconfig)
			tokenResult, err := e.MasterRunner.Run(ctx, tokenCommand)
			if err != nil {
				return AddMasterResult{}, commandError("create kubeadm bootstrap token", tokenResult, err)
			}
			credentials, err := kubeadm.ParseJoinCommand(tokenResult.Stdout)
			if err != nil {
				return AddMasterResult{}, err
			}
			if credentials.APIServerEndpoint != configuration.Spec.ControlPlane.Endpoint {
				return AddMasterResult{}, fmt.Errorf("kubeadm join endpoint %q does not match configured endpoint %q", credentials.APIServerEndpoint, configuration.Spec.ControlPlane.Endpoint)
			}
			joinConfig, err := kubeadm.GenerateControlPlaneJoinConfig(credentials, nodeName, target.Address, certificateKey)
			if err != nil {
				return AddMasterResult{}, err
			}
			if err := uploadGeneratedFile(ctx, e.Remote, e.RemoteJoinConfig, joinConfig); err != nil {
				return AddMasterResult{}, err
			}
			if err := saveAddState(statePath, state, AddPhaseJoinStarting); err != nil {
				return AddMasterResult{}, err
			}
			state.Phase = AddPhaseJoinStarting
			joinConfigPath := quoteShell(e.RemoteJoinConfig)
			joinResult, err = e.Remote.Run(ctx, "trap \"rm -f -- "+joinConfigPath+"\" EXIT; /usr/bin/kubeadm join --ignore-preflight-errors=FileExisting-conntrack --config "+joinConfigPath)
			if err != nil {
				return AddMasterResult{}, commandError("join control-plane node", joinResult, err)
			}
			joined, err = remoteNodeJoined(ctx, e.Remote)
			if err != nil {
				return AddMasterResult{}, err
			}
			if !joined {
				return AddMasterResult{}, fmt.Errorf("kubeadm join succeeded but target kubelet.conf was not created")
			}
		}
		if err := saveAddState(statePath, state, AddPhaseJoined); err != nil {
			return AddMasterResult{}, err
		}
		state.Phase = AddPhaseJoined
	}

	resources := []struct{ resource, description string }{
		{"node/" + nodeName, "control-plane node readiness"},
		{"pod/kube-apiserver-" + nodeName, "new API server readiness"},
		{"pod/etcd-" + nodeName, "new etcd member readiness"},
	}
	if !state.reached(AddPhaseReady) {
		for _, item := range resources {
			command := "/usr/bin/kubectl --kubeconfig " + quoteShell(e.AdminKubeconfig)
			if strings.HasPrefix(item.resource, "pod/") {
				command += " -n kube-system"
			}
			command += " wait --for=condition=Ready " + quoteShell(item.resource) + " --timeout=10m"
			if result, err := e.MasterRunner.Run(ctx, command); err != nil {
				return AddMasterResult{}, commandError("wait for "+item.description, result, err)
			}
		}
		if err := saveAddState(statePath, state, AddPhaseReady); err != nil {
			return AddMasterResult{}, err
		}
		state.Phase = AddPhaseReady
	}
	if err := saveAddState(statePath, state, AddPhaseComplete); err != nil {
		return AddMasterResult{}, err
	}
	payloadCount := 0
	if staged != nil {
		payloadCount = len(staged.Files)
	}
	return AddMasterResult{
		NodeName: nodeName, PayloadCount: payloadCount, Preparation: preparation,
		Images: images, KubeadmOutput: strings.TrimSpace(joinResult.Stdout), Phase: AddPhaseComplete,
	}, nil
}
