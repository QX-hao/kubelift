package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/remote"
)

type addMasterControlPlane struct {
	commands []string
}

func (r *addMasterControlPlane) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(command, "get nodes -o json") {
		return remote.CommandResult{Stdout: `{"items":[]}`}, nil
	}
	if strings.Contains(command, "upload-certs") {
		return remote.CommandResult{Stdout: "[upload-certs] Using certificate key:\nabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n"}, nil
	}
	if strings.Contains(command, "token create") {
		return remote.CommandResult{Stdout: "kubeadm join 10.0.0.100:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"}, nil
	}
	return remote.CommandResult{}, nil
}

func TestAddMasterExecutorPreparesAndJoinsControlPlane(t *testing.T) {
	configuration := createTestConfig(t)
	configuration.Spec.ControlPlane.Endpoint = "10.0.0.100:6443"
	root := t.TempDir()
	remoteNode := &addNodeRemote{}
	master0 := &addMasterControlPlane{}
	executor := AddMasterExecutor{
		MasterRunner: master0, Remote: remoteNode,
		RemoteRoot: filepath.Join(root, "staging"), RemoteJoinConfig: filepath.Join(root, "kubernetes", "join.yaml"),
		AdminKubeconfig: filepath.Join(root, "master", "admin.conf"),
		StateRoot:       filepath.Join(root, "state"),
	}
	target := Target{Address: "10.0.0.11", Name: "master-1", Role: RoleMaster, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}

	result, err := executor.Execute(context.Background(), configuration, target, JoinOptions{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.NodeName != "master-1" || result.Images.KubernetesCount != 1 || result.Images.CiliumCount != 1 || result.Images.RegistryCount != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(master0.commands) != 6 {
		t.Fatalf("Master0 commands = %v, want inventory, upload, token and three readiness checks", master0.commands)
	}
	for _, expected := range []string{"upload-certs", "token create", "node/master-1", "pod/kube-apiserver-master-1", "pod/etcd-master-1"} {
		found := false
		for _, command := range master0.commands {
			found = found || strings.Contains(command, expected)
		}
		if !found {
			t.Errorf("Master0 commands do not contain %q: %v", expected, master0.commands)
		}
	}
	joinData, err := filepath.Glob(executor.RemoteJoinConfig)
	if err != nil || len(joinData) != 1 {
		t.Fatalf("join configuration = %v, error = %v", joinData, err)
	}
}

func TestAddMasterExecutorRequiresStableEndpoint(t *testing.T) {
	configuration := createTestConfig(t)
	executor := AddMasterExecutor{MasterRunner: &addMasterControlPlane{}, Remote: &addNodeRemote{}}
	target := Target{Address: "10.0.0.11", Name: "master-1", Role: RoleMaster, SSHUser: "root", SSHPort: 22, PrivateKey: "/root/.ssh/id_ed25519"}

	_, err := executor.Execute(context.Background(), configuration, target, JoinOptions{})
	if err == nil || !strings.Contains(err.Error(), "endpoint is required") {
		t.Fatalf("Execute() error = %v, want endpoint error", err)
	}
}
