package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/remote"
)

type addNodeRemote struct {
	distribute.LocalTransport
	commands      []string
	alreadyJoined bool
	joined        bool
}

func (r *addNodeRemote) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	switch command {
	case "if [ -s /etc/kubernetes/kubelet.conf ]; then printf joined; else printf absent; fi":
		if r.alreadyJoined || r.joined {
			return remote.CommandResult{Stdout: "joined"}, nil
		}
		return remote.CommandResult{Stdout: "absent"}, nil
	case "hostname":
		return remote.CommandResult{Stdout: "K8S3\n"}, nil
	case "test -s /etc/kubernetes/kubelet.conf":
		return remote.CommandResult{}, nil
	}
	if strings.Contains(command, "/usr/bin/kubeadm join") {
		r.joined = true
		return remote.CommandResult{Stdout: "node joined\n"}, nil
	}
	return remote.CommandResult{}, nil
}

type addNodeMaster struct {
	commands []string
}

func (r *addNodeMaster) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(command, "get nodes -o json") {
		return remote.CommandResult{Stdout: `{"items":[]}`}, nil
	}
	if strings.Contains(command, "token create") {
		return remote.CommandResult{Stdout: "kubeadm join 10.0.0.100:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"}, nil
	}
	return remote.CommandResult{}, nil
}

func TestAddNodeExecutorPreparesAndJoinsWorker(t *testing.T) {
	configuration := createTestConfig(t)
	configuration.Spec.ControlPlane.Endpoint = "10.0.0.100:6443"
	root := t.TempDir()
	worker := &addNodeRemote{}
	master := &addNodeMaster{}
	executor := AddNodeExecutor{
		MasterRunner:     master,
		Remote:           worker,
		RemoteRoot:       filepath.Join(root, "staging"),
		RemoteJoinConfig: filepath.Join(root, "kubernetes", "join.yaml"),
		AdminKubeconfig:  filepath.Join(root, "master", "admin.conf"),
		StateRoot:        filepath.Join(root, "state"),
	}
	target := Target{Address: "10.0.0.21", Role: RoleNode, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}

	result, err := executor.Execute(context.Background(), configuration, target, JoinOptions{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.NodeName != "k8s3" || result.Images.KubernetesCount != 1 || result.Images.CiliumCount != 1 || result.Images.RegistryCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	joinContents, err := filepath.Glob(executor.RemoteJoinConfig)
	if err != nil || len(joinContents) != 1 {
		t.Fatalf("join configuration = %v, error = %v", joinContents, err)
	}
	foundJoin := false
	for _, command := range worker.commands {
		if strings.Contains(command, "/usr/bin/kubeadm join --ignore-preflight-errors=FileExisting-conntrack --config") && strings.Contains(command, "trap \"rm -f") {
			foundJoin = true
		}
		if strings.Contains(command, "registry.tar") {
			t.Fatalf("worker imported Registry image: %s", command)
		}
	}
	if !foundJoin || len(master.commands) != 3 || !strings.Contains(master.commands[2], "wait --for=condition=Ready") {
		t.Fatalf("worker commands = %v, master commands = %v", worker.commands, master.commands)
	}
	if _, err := executor.Execute(context.Background(), configuration, target, JoinOptions{}); err == nil || !strings.Contains(err.Error(), "use --resume") {
		t.Fatalf("second add error = %v, want explicit resume error", err)
	}
	commandCount := len(worker.commands)
	resumed, err := executor.Execute(context.Background(), configuration, target, JoinOptions{Resume: true})
	if err != nil || !resumed.AlreadyComplete || len(worker.commands) != commandCount+2 {
		t.Fatalf("completed resume = %+v, error = %v, commands = %v", resumed, err, worker.commands)
	}
}

func TestAddNodeExecutorRejectsDuplicateClusterNodeBeforeStaging(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	worker := &addNodeRemote{}
	master := &addNodeMasterWithInventory{output: `{"items":[{"metadata":{"name":"existing"},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.21"}]}}]}`}
	executor := AddNodeExecutor{
		MasterRunner: master, Remote: worker, RemoteRoot: filepath.Join(root, "staging"),
		RemoteJoinConfig: filepath.Join(root, "join.yaml"), AdminKubeconfig: filepath.Join(root, "admin.conf"), StateRoot: filepath.Join(root, "state"),
	}
	target := Target{Address: "10.0.0.21", Name: "k8s3", Role: RoleNode, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}

	_, err := executor.Execute(context.Background(), configuration, target, JoinOptions{})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("Execute() error = %v, want duplicate address error", err)
	}
}

type addNodeMasterWithInventory struct{ output string }

func (r *addNodeMasterWithInventory) Run(_ context.Context, command string) (remote.CommandResult, error) {
	if strings.Contains(command, "get nodes -o json") {
		return remote.CommandResult{Stdout: r.output}, nil
	}
	return remote.CommandResult{}, nil
}

func TestAddNodeExecutorRejectsAmbiguousJoinResume(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	worker := &addNodeRemote{}
	executor := AddNodeExecutor{
		MasterRunner: &addNodeMaster{}, Remote: worker,
		RemoteRoot: filepath.Join(root, "staging"), RemoteJoinConfig: filepath.Join(root, "join.yaml"),
		AdminKubeconfig: filepath.Join(root, "admin.conf"), StateRoot: filepath.Join(root, "state"),
	}
	target := Target{Address: "10.0.0.21", Name: "k8s3", Role: RoleNode, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}
	configHash, err := configurationHash(configuration)
	if err != nil {
		t.Fatalf("configurationHash() error = %v", err)
	}
	bundleHash, err := fileSHA256(configuration.Spec.Offline.Bundle)
	if err != nil {
		t.Fatalf("fileSHA256() error = %v", err)
	}
	state := addState{
		APIVersion: addStateAPIVersion, Kind: addStateKind, Cluster: configuration.Metadata.Name,
		KubernetesVersion: configuration.Spec.Kubernetes.Version, Role: target.Role,
		Address: target.Address, NodeName: target.Name, ConfigurationSHA256: configHash, BundleSHA256: bundleHash,
	}
	statePath := addStatePath(executor.StateRoot, configuration.Metadata.Name, target.Role, target.Address)
	if err := saveAddState(statePath, state, AddPhaseJoinStarting); err != nil {
		t.Fatalf("saveAddState() error = %v", err)
	}

	_, err = executor.Execute(context.Background(), configuration, target, JoinOptions{Resume: true})
	if err == nil || !strings.Contains(err.Error(), "may be incomplete") {
		t.Fatalf("Execute() error = %v, want ambiguous join error", err)
	}
}

func TestAddNodeExecutorRejectsJoinedWorkerBeforeStaging(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	worker := &addNodeRemote{alreadyJoined: true}
	executor := AddNodeExecutor{
		MasterRunner: &addNodeMaster{}, Remote: worker,
		RemoteRoot: filepath.Join(root, "staging"), RemoteJoinConfig: filepath.Join(root, "join.yaml"),
		AdminKubeconfig: filepath.Join(root, "admin.conf"),
		StateRoot:       filepath.Join(root, "state"),
	}
	target := Target{Address: "10.0.0.21", Name: "k8s3", Role: RoleNode, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}

	_, err := executor.Execute(context.Background(), configuration, target, JoinOptions{})
	if err == nil || !strings.Contains(err.Error(), "already appears joined") {
		t.Fatalf("Execute() error = %v, want already joined error", err)
	}
	if len(worker.commands) != 1 {
		t.Fatalf("commands = %v, want only target state check", worker.commands)
	}
}
