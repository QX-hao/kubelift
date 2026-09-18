package preflight

import (
	"context"
	"fmt"
	"strings"
)

// PrepareSwap 关闭远程节点当前启用的 swap，并持久化禁用配置。
// 只在实际安装流程中调用；check ssh 保持只读。
func PrepareSwap(ctx context.Context, runner RemoteRunner) Result {
	result := Result{Name: "swap"}
	command := "set -eu; if [ -n \"$(swapon --noheadings --show)\" ]; then swapoff -a; fi; if [ -f /etc/fstab ]; then if [ ! -e /etc/fstab.kubelift.bak ]; then cp -a -- /etc/fstab /etc/fstab.kubelift.bak; fi; sed -i -E '/^[[:space:]]*[^#].*[[:space:]]swap([[:space:]]|$)/ s/^/#/' /etc/fstab; fi; systemctl daemon-reload; if [ -n \"$(swapon --noheadings --show)\" ]; then echo 'swap is still enabled after preparation' >&2; exit 1; fi; printf disabled"
	commandResult, err := runner.Run(ctx, command)
	if err != nil {
		if stderr := strings.TrimSpace(commandResult.Stderr); stderr != "" {
			result.Err = fmt.Errorf("%w; remote stderr: %s", err, stderr)
		} else {
			result.Err = err
		}
		return result
	}
	result.Detail = strings.TrimSpace(commandResult.Stdout)
	if result.Detail == "" {
		result.Detail = "disabled"
	}
	return result
}
