package clusterstatus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type runner interface {
	combinedOutput(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func Run(ctx context.Context, kubeconfigPath string) ([]byte, error) {
	return run(ctx, kubeconfigPath, commandRunner{})
}

// RunDetails 查询节点以及 KubeLift 安装的关键系统组件。
func RunDetails(ctx context.Context, kubeconfigPath string, registryEnabled bool) ([]byte, error) {
	return runDetails(ctx, kubeconfigPath, registryEnabled, commandRunner{})
}

func run(ctx context.Context, kubeconfigPath string, command runner) ([]byte, error) {
	if err := validateKubeconfig(kubeconfigPath); err != nil {
		return nil, err
	}

	// status 只执行只读查询，不在该命令中修复或重启任何集群组件。
	output, err := command.combinedOutput(
		ctx,
		"kubectl",
		"--kubeconfig",
		kubeconfigPath,
		"get",
		"nodes",
		"-o",
		"wide",
	)
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return nil, fmt.Errorf("query cluster nodes: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("query cluster nodes: %w", err)
	}
	return output, nil
}

func runDetails(ctx context.Context, kubeconfigPath string, registryEnabled bool, command runner) ([]byte, error) {
	if err := validateKubeconfig(kubeconfigPath); err != nil {
		return nil, err
	}
	type query struct {
		title string
		name  string
		args  []string
	}
	queries := []query{
		{title: "Nodes", name: "cluster nodes", args: []string{"get", "nodes", "-o", "wide"}},
		{title: "Cilium", name: "Cilium pods", args: []string{"-n", "kube-system", "get", "pods", "-l", "k8s-app=cilium", "-o", "wide"}},
		{title: "CoreDNS", name: "CoreDNS pods", args: []string{"-n", "kube-system", "get", "pods", "-l", "k8s-app=kube-dns", "-o", "wide"}},
	}
	if registryEnabled {
		queries = append(queries, query{title: "Registry", name: "Registry pod", args: []string{"-n", "kube-system", "get", "pods", "-l", "app.kubernetes.io/name=kubelift-registry", "-o", "wide"}})
	}

	var report strings.Builder
	for index, item := range queries {
		args := append([]string{"--kubeconfig", kubeconfigPath}, item.args...)
		output, err := command.combinedOutput(ctx, "kubectl", args...)
		if err != nil {
			detail := strings.TrimSpace(string(output))
			if detail != "" {
				return nil, fmt.Errorf("query %s: %w: %s", item.name, err, detail)
			}
			return nil, fmt.Errorf("query %s: %w", item.name, err)
		}
		if index > 0 {
			report.WriteByte('\n')
		}
		fmt.Fprintf(&report, "[%s]\n", item.title)
		report.Write(output)
		if len(output) == 0 || output[len(output)-1] != '\n' {
			report.WriteByte('\n')
		}
	}
	return []byte(report.String()), nil
}

func validateKubeconfig(kubeconfigPath string) error {
	info, err := os.Stat(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("stat kubeconfig %q: %w", kubeconfigPath, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("kubeconfig %q must be a non-empty regular file", kubeconfigPath)
	}
	return nil
}

func (commandRunner) combinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
