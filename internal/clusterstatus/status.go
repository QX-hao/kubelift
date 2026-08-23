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

func run(ctx context.Context, kubeconfigPath string, command runner) ([]byte, error) {
	info, err := os.Stat(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("stat kubeconfig %q: %w", kubeconfigPath, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("kubeconfig %q must be a non-empty regular file", kubeconfigPath)
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

func (commandRunner) combinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
