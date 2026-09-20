package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/remote"
)

type uninstallRunner struct {
	commands []string
	nodes    string
}

func (r *uninstallRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(command, "get nodes -o json") {
		return remote.CommandResult{Stdout: r.nodes}, nil
	}
	return remote.CommandResult{}, nil
}

type uninstallRemote struct {
	runner *uninstallRunner
	closed bool
}

func (r *uninstallRemote) Run(ctx context.Context, command string) (remote.CommandResult, error) {
	return r.runner.Run(ctx, command)
}

func (r *uninstallRemote) Close() error {
	r.closed = true
	return nil
}

func TestDiscoverUninstallNodesAndOrder(t *testing.T) {
	runner := &uninstallRunner{nodes: `{"items":[
{"metadata":{"name":"k8s3","labels":{}},"status":{"addresses":[{"type":"InternalIP","address":"192.168.121.153"}]}},
{"metadata":{"name":"k8s2","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"addresses":[{"type":"InternalIP","address":"192.168.121.152"}]}},
{"metadata":{"name":"k8s1","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"addresses":[{"type":"InternalIP","address":"192.168.121.151"}]}}
]}`}
	nodes, err := DiscoverUninstallNodes(context.Background(), runner, "/etc/kubernetes/admin.conf", "192.168.121.151")
	if err != nil {
		t.Fatalf("DiscoverUninstallNodes() error = %v", err)
	}
	ordered, err := orderUninstallNodes(nodes)
	if err != nil {
		t.Fatalf("orderUninstallNodes() error = %v", err)
	}
	want := []string{"k8s3", "k8s2", "k8s1"}
	for index, node := range ordered {
		if node.Name != want[index] {
			t.Fatalf("ordered[%d] = %q, want %q", index, node.Name, want[index])
		}
	}
	if !ordered[2].Local || ordered[2].Role != UninstallRoleControlPlane {
		t.Fatalf("local node = %+v, want local control plane", ordered[2])
	}
}

func TestExecuteUninstallResetsRemoteNodesBeforeLocalNode(t *testing.T) {
	configuration := createTestConfig(t)
	runner := &uninstallRunner{nodes: `{"items":[
{"metadata":{"name":"k8s3","labels":{}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.12"}]}},
{"metadata":{"name":"k8s2","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.11"}]}},
{"metadata":{"name":"k8s1","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.10"}]}}
]}`}
	connections := make([]string, 0, 2)
	executor := UninstallExecutor{
		Runner:          runner,
		AdminKubeconfig: "/etc/kubernetes/admin.conf",
		EffectiveUserID: func() int { return 0 },
		Connector: func(_ context.Context, target remote.Target) (UninstallRemote, error) {
			connections = append(connections, target.Address)
			return &uninstallRemote{runner: runner}, nil
		},
	}
	result, err := executor.ExecuteUninstall(context.Background(), configuration, UninstallOptions{})
	if err != nil {
		t.Fatalf("ExecuteUninstall() error = %v", err)
	}
	if len(result.Nodes) != 3 || len(connections) != 2 || connections[0] != "10.0.0.12" || connections[1] != "10.0.0.11" {
		t.Fatalf("result = %+v, connections = %v", result, connections)
	}
	if len(runner.commands) != 6 {
		t.Fatalf("commands = %v, want discovery, 2 remote cleanup, 2 deletes, local cleanup", runner.commands)
	}
	if !strings.Contains(runner.commands[1], "kubeadm reset") || !strings.Contains(runner.commands[2], "delete node 'k8s3'") ||
		!strings.Contains(runner.commands[3], "kubeadm reset") || !strings.Contains(runner.commands[4], "delete node 'k8s2'") ||
		!strings.Contains(runner.commands[5], "kubeadm reset") {
		t.Fatalf("unexpected uninstall command order: %v", runner.commands)
	}
}

func TestCleanupCommandPreservesConfigUnlessPurged(t *testing.T) {
	command := CleanupCommand(false)
	if strings.Contains(command, "rm -rf -- /etc/kubelift") {
		t.Fatal("default cleanup must preserve /etc/kubelift")
	}
	if !strings.Contains(CleanupCommand(true), "rm -rf -- /etc/kubelift") {
		t.Fatal("purge cleanup must remove /etc/kubelift")
	}
}

func TestCleanupCommandHasValidShellSyntax(t *testing.T) {
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(CleanupCommand(true))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CleanupCommand() shell syntax error = %v: %s", err, output)
	}
}

func TestDiscoverUninstallNodesRejectsMissingLocalNode(t *testing.T) {
	runner := &uninstallRunner{nodes: `{"items":[{"metadata":{"name":"k8s2","labels":{}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.11"}]}}]}`}
	_, err := DiscoverUninstallNodes(context.Background(), runner, "/etc/kubernetes/admin.conf", "10.0.0.10")
	if err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("error = %v, want missing local node error", err)
	}
}

func TestUninstallPlan(t *testing.T) {
	configuration := createTestConfig(t)
	plan := UninstallPlan(configuration)
	if plan.Action != "uninstall cluster" || len(plan.Steps) != 5 {
		t.Fatalf("plan = %+v", plan)
	}
	if fmt.Sprint(plan.Steps[0].Name) != "discover-nodes" {
		t.Fatalf("first step = %+v", plan.Steps[0])
	}
}
