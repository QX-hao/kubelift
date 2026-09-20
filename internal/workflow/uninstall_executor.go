/*
Copyright © 2026 QX-hao

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/install"
	"github.com/QX-hao/kubelift/internal/localexec"
	"github.com/QX-hao/kubelift/internal/remote"
)

const (
	defaultUninstallAdminKubeconfig = "/etc/kubernetes/admin.conf"
)

// UninstallRole 是节点在 Kubernetes 集群中的角色。
type UninstallRole string

const (
	UninstallRoleWorker       UninstallRole = "worker"
	UninstallRoleControlPlane UninstallRole = "control-plane"
)

// UninstallNode 描述卸载时需要处理的一个 Kubernetes 节点。
type UninstallNode struct {
	Name    string
	Address string
	Role    UninstallRole
	Local   bool
}

// UninstallOptions 控制卸载时是否删除本机的 KubeLift 配置目录。
type UninstallOptions struct {
	PurgeConfig bool
}

// UninstallRemote 是卸载器所需的远程 SSH 客户端最小接口。
type UninstallRemote interface {
	install.CommandRunner
	Close() error
}

// UninstallConnector 建立一个已完成主机指纹校验的 SSH 连接。
type UninstallConnector func(context.Context, remote.Target) (UninstallRemote, error)

// UninstallExecutor 在当前控制面发现并清理整个集群。
type UninstallExecutor struct {
	Runner          install.CommandRunner
	Connector       UninstallConnector
	AdminKubeconfig string
	EffectiveUserID func() int
}

// NewLocalUninstallExecutor 返回运行在当前 Master0 上的卸载器。
func NewLocalUninstallExecutor() UninstallExecutor {
	return UninstallExecutor{
		Runner:          localexec.Runner{},
		Connector:       connectUninstallRemote,
		AdminKubeconfig: defaultUninstallAdminKubeconfig,
		EffectiveUserID: os.Geteuid,
	}
}

func connectUninstallRemote(ctx context.Context, target remote.Target) (UninstallRemote, error) {
	return remote.Connect(ctx, target)
}

// ExecuteUninstall 先清理远程节点，再清理当前控制面。这样 API Server 在发现和
// 删除远程节点期间仍然可用；重复执行时 kubeadm reset 和受管路径删除保持幂等。
func (e UninstallExecutor) ExecuteUninstall(ctx context.Context, configuration config.Config, options UninstallOptions) (UninstallResult, error) {
	if e.Runner == nil || e.Connector == nil || e.EffectiveUserID == nil {
		return UninstallResult{}, fmt.Errorf("uninstall executor dependencies are incomplete")
	}
	if e.EffectiveUserID() != 0 {
		return UninstallResult{}, fmt.Errorf("cluster uninstallation must run as root")
	}
	if !filepath.IsAbs(e.AdminKubeconfig) || filepath.Clean(e.AdminKubeconfig) == string(filepath.Separator) {
		return UninstallResult{}, fmt.Errorf("admin kubeconfig must be an absolute non-root path")
	}
	nodes, err := DiscoverUninstallNodes(ctx, e.Runner, e.AdminKubeconfig, configuration.Spec.ControlPlane.AdvertiseAddress)
	if err != nil {
		return UninstallResult{}, err
	}
	ordered, err := orderUninstallNodes(nodes)
	if err != nil {
		return UninstallResult{}, err
	}

	for _, node := range ordered {
		if node.Local {
			continue
		}
		target := remote.Target{
			Address:        node.Address,
			User:           configuration.Spec.SSH.User,
			Port:           configuration.Spec.SSH.Port,
			PrivateKeyPath: configuration.Spec.SSH.PrivateKey,
		}
		client, err := e.Connector(ctx, target)
		if err != nil {
			return UninstallResult{}, fmt.Errorf("connect to %s (%s): %w", node.Name, node.Address, err)
		}
		cleanupResult, cleanupErr := client.Run(ctx, CleanupCommand(false))
		closeErr := client.Close()
		if cleanupErr != nil {
			return UninstallResult{}, commandError("uninstall node "+node.Name, cleanupResult, cleanupErr)
		}
		if closeErr != nil {
			return UninstallResult{}, fmt.Errorf("close SSH connection to %s: %w", node.Name, closeErr)
		}
		if err := deleteUninstallNode(ctx, e.Runner, e.AdminKubeconfig, node.Name); err != nil {
			return UninstallResult{}, err
		}
	}

	cleanupResult, cleanupErr := e.Runner.Run(ctx, CleanupCommand(options.PurgeConfig))
	if cleanupErr != nil {
		return UninstallResult{}, commandError("uninstall local control plane", cleanupResult, cleanupErr)
	}
	return UninstallResult{Nodes: ordered}, nil
}

// UninstallResult 汇总实际发现并处理的节点。
type UninstallResult struct {
	Nodes []UninstallNode
}

// DiscoverUninstallNodes 从当前控制面的 API Server 读取节点清单，不修改集群。
func DiscoverUninstallNodes(ctx context.Context, runner install.CommandRunner, kubeconfigPath, localAddress string) ([]UninstallNode, error) {
	if runner == nil {
		return nil, fmt.Errorf("uninstall discovery command runner is required")
	}
	if !filepath.IsAbs(kubeconfigPath) || filepath.Clean(kubeconfigPath) == string(filepath.Separator) {
		return nil, fmt.Errorf("admin kubeconfig must be an absolute non-root path")
	}
	local, err := netip.ParseAddr(localAddress)
	if err != nil || !local.Is4() {
		return nil, fmt.Errorf("configured advertise address must be a valid IPv4 address")
	}
	command := "/usr/bin/kubectl --kubeconfig " + quoteShell(kubeconfigPath) + " get nodes -o json"
	result, err := runner.Run(ctx, command)
	if err != nil {
		return nil, commandError("discover Kubernetes nodes", result, err)
	}
	var response struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return nil, fmt.Errorf("decode Kubernetes node list: %w", err)
	}
	if len(response.Items) == 0 {
		return nil, fmt.Errorf("Kubernetes node list is empty")
	}

	nodes := make([]UninstallNode, 0, len(response.Items))
	seenNames := make(map[string]struct{}, len(response.Items))
	seenAddresses := make(map[string]struct{}, len(response.Items))
	localFound := false
	for _, item := range response.Items {
		name := strings.TrimSpace(item.Metadata.Name)
		if name == "" {
			return nil, fmt.Errorf("Kubernetes node list contains a node without a name")
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("Kubernetes node list contains duplicate node %q", name)
		}
		address := ""
		for _, candidate := range item.Status.Addresses {
			if candidate.Type == "InternalIP" {
				address = strings.TrimSpace(candidate.Address)
				break
			}
		}
		parsedAddress, err := netip.ParseAddr(address)
		if err != nil || !parsedAddress.Is4() {
			return nil, fmt.Errorf("Kubernetes node %q has no usable IPv4 InternalIP", name)
		}
		address = parsedAddress.String()
		if _, exists := seenAddresses[address]; exists {
			return nil, fmt.Errorf("Kubernetes node list contains duplicate InternalIP %q", address)
		}
		role := UninstallRoleWorker
		if hasControlPlaneLabel(item.Metadata.Labels) {
			role = UninstallRoleControlPlane
		}
		isLocal := parsedAddress == local

		seenNames[name] = struct{}{}
		seenAddresses[address] = struct{}{}
		if isLocal {
			localFound = true
			if role != UninstallRoleControlPlane {
				return nil, fmt.Errorf("configured advertise address %q belongs to a worker node %q", address, name)
			}
		}
		nodes = append(nodes, UninstallNode{Name: name, Address: address, Role: role, Local: isLocal})
	}
	if !localFound {
		return nil, fmt.Errorf("configured advertise address %q was not found in the Kubernetes node list", local.String())
	}
	return nodes, nil
}

func hasControlPlaneLabel(labels map[string]string) bool {
	_, controlPlane := labels["node-role.kubernetes.io/control-plane"]
	_, legacyMaster := labels["node-role.kubernetes.io/master"]
	return controlPlane || legacyMaster
}

func orderUninstallNodes(nodes []UninstallNode) ([]UninstallNode, error) {
	ordered := append([]UninstallNode(nil), nodes...)
	localCount := 0
	for _, node := range ordered {
		if node.Local {
			localCount++
		}
	}
	if localCount != 1 {
		return nil, fmt.Errorf("Kubernetes node list must contain exactly one local control-plane node, found %d", localCount)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftRank := uninstallNodeRank(ordered[left])
		rightRank := uninstallNodeRank(ordered[right])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return ordered[left].Name < ordered[right].Name
	})
	return ordered, nil
}

func uninstallNodeRank(node UninstallNode) int {
	if node.Local {
		return 2
	}
	if node.Role == UninstallRoleWorker {
		return 0
	}
	return 1
}

func deleteUninstallNode(ctx context.Context, runner install.CommandRunner, kubeconfigPath, name string) error {
	command := "/usr/bin/kubectl --kubeconfig " + quoteShell(kubeconfigPath) + " delete node " + quoteShell(name) + " --ignore-not-found=true"
	result, err := runner.Run(ctx, command)
	if err != nil {
		return commandError("delete Kubernetes node "+name, result, err)
	}
	return nil
}

// CleanupCommand 返回在一个节点上执行的幂等清理脚本。
// dpkg-query 检查用于避免删除 Ubuntu 软件包实际拥有的同名文件。
func CleanupCommand(purgeConfig bool) string {
	steps := []string{
		"set -eu",
		"if [ -x /usr/bin/kubeadm ] && { [ -e /etc/kubernetes/kubelet.conf ] || [ -e /etc/kubernetes/admin.conf ] || [ -d /etc/kubernetes/pki ]; }; then /usr/bin/kubeadm reset -f --cri-socket unix:///run/containerd/containerd.sock; fi",
		"systemctl disable --now kubelet.service 2>/dev/null || true",
		"systemctl disable --now containerd.service 2>/dev/null || true",
		"for link in cilium_net cilium_host cilium_vxlan; do if ip link show \"$link\" >/dev/null 2>&1; then ip link delete \"$link\" 2>/dev/null || true; fi; done",
		"for link in /sys/class/net/lxc*; do if [ -e \"$link\" ]; then ip link delete \"$(basename \"$link\")\" 2>/dev/null || true; fi; done",
		"rm -rf -- /etc/kubernetes /var/lib/kubelet /var/lib/etcd /var/lib/cni /var/lib/containerd /run/containerd /var/lib/kubelift",
		"rm -rf -- /etc/cni/net.d",
		"rm -f -- /opt/cni/bin/cilium-cni /opt/cni/bin/cilium-health /opt/cni/bin/cilium-dbg",
		"rm -f -- /etc/containerd/config.toml /etc/containerd/certs.d/docker.io/hosts.toml",
		"rmdir --ignore-fail-on-non-empty /etc/containerd/certs.d/docker.io /etc/containerd/certs.d /etc/containerd 2>/dev/null || true",
		"rm -f -- /etc/systemd/system/kubelet.service /etc/systemd/system/containerd.service /etc/modules-load.d/kubelift.conf /etc/sysctl.d/99-kubelift.conf",
		"remove_if_unowned() { for path in \"$@\"; do if [ -e \"$path\" ] && ! dpkg-query -S \"$path\" >/dev/null 2>&1; then rm -f -- \"$path\"; fi; done; }",
		"remove_if_unowned /usr/bin/kubeadm /usr/bin/kubelet /usr/bin/kubectl /usr/bin/crictl /usr/bin/conntrack /usr/bin/ethtool /usr/bin/iptables /usr/bin/iptables-legacy /usr/bin/iptables-nft /usr/bin/ip6tables /usr/bin/ip6tables-legacy /usr/bin/ip6tables-nft /usr/bin/runc /usr/bin/containerd /usr/bin/ctr /usr/bin/containerd-shim /usr/bin/containerd-shim-runc-v1 /usr/bin/containerd-shim-runc-v2",
		"systemctl daemon-reload",
		"sysctl --system >/dev/null 2>&1 || true",
	}
	if purgeConfig {
		steps = append(steps, "rm -rf -- /etc/kubelift")
	}
	return strings.Join(steps, " && ")
}
