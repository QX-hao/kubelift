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
package install

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

// CommandRunner 是节点准备步骤需要的最小远程命令接口。
type CommandRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
}

// Report 描述一次节点准备执行的载荷数量。
type Report struct {
	BinaryCount  int
	RuntimeCount int
	ConfigCount  int
	UnitCount    int
}

// PrepareNode 使用 Sealos 风格的 Bundle 载荷准备一个 Ubuntu 节点。
// 该步骤只安装运行时和服务文件，不初始化 Kubernetes 集群。
func PrepareNode(ctx context.Context, runner CommandRunner, remoteRoot string, manifest bundle.Manifest) (Report, error) {
	if runner == nil {
		return Report{}, fmt.Errorf("node preparation command runner is required")
	}
	if !path.IsAbs(remoteRoot) || path.Clean(remoteRoot) == "/" {
		return Report{}, fmt.Errorf("remote bundle directory must be an absolute non-root path")
	}
	if err := manifest.Validate(); err != nil {
		return Report{}, fmt.Errorf("validate offline bundle manifest: %w", err)
	}

	steps := make([]string, 0, len(manifest.Spec.Files)+8)
	steps = append(steps,
		"set -eu",
		"install -d -m 0755 -- /usr/bin /opt/cni/bin /etc/containerd /etc/systemd/system /etc/modules-load.d /etc/sysctl.d",
		"printf '%s' "+shellQuote("overlay\nbr_netfilter\n")+" > /etc/modules-load.d/kubelift.conf",
		"modprobe overlay",
		"modprobe br_netfilter",
		"printf '%s' "+shellQuote("net.bridge.bridge-nf-call-iptables = 1\nnet.bridge.bridge-nf-call-ip6tables = 1\nnet.ipv4.ip_forward = 1\n")+" > /etc/sysctl.d/99-kubelift.conf",
		"sysctl --system",
	)
	report := Report{}

	for _, role := range []string{"kubeadm", "kubelet", "kubectl", "runc"} {
		files := manifest.FilesForRole(role)
		if role != "runc" && len(files) != 1 {
			return Report{}, fmt.Errorf("offline bundle must contain exactly one %q binary, found %d", role, len(files))
		}
		if len(files) == 0 {
			continue
		}
		file := files[0]
		remotePath, err := remotePayloadPath(remoteRoot, file.Path)
		if err != nil {
			return Report{}, err
		}
		steps = append(steps, "install -m 0755 -- "+shellQuote(remotePath)+" "+shellQuote("/usr/bin/"+role))
		report.BinaryCount++
	}

	for _, role := range []string{"cri-tool", "cni-plugin"} {
		files := append([]bundle.File(nil), manifest.FilesForRole(role)...)
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		for _, file := range files {
			remotePath, err := remotePayloadPath(remoteRoot, file.Path)
			if err != nil {
				return Report{}, err
			}
			name := filepath.Base(filepath.FromSlash(file.Path))
			destination := "/usr/bin/" + name
			if role == "cni-plugin" {
				destination = "/opt/cni/bin/" + name
			}
			steps = append(steps, "install -m 0755 -- "+shellQuote(remotePath)+" "+shellQuote(destination))
			report.BinaryCount++
		}
	}

	containerdFiles := manifest.FilesForRole("containerd")
	if len(containerdFiles) != 1 {
		return Report{}, fmt.Errorf("offline bundle must contain exactly one containerd runtime archive, found %d", len(containerdFiles))
	}
	containerdPath, err := remotePayloadPath(remoteRoot, containerdFiles[0].Path)
	if err != nil {
		return Report{}, err
	}
	steps = append(steps, "tar -xzf "+shellQuote(containerdPath)+" --strip-components=2 -C /usr/bin")
	report.RuntimeCount = 1

	configFiles := manifest.FilesForRole("containerd-config")
	if len(configFiles) != 1 {
		return Report{}, fmt.Errorf("offline bundle must contain exactly one containerd config, found %d", len(configFiles))
	}
	configPath, err := remotePayloadPath(remoteRoot, configFiles[0].Path)
	if err != nil {
		return Report{}, err
	}
	steps = append(steps, "install -m 0644 -- "+shellQuote(configPath)+" /etc/containerd/config.toml")
	report.ConfigCount++

	unitFiles := append([]bundle.File(nil), manifest.FilesForRole("systemd-unit")...)
	sort.Slice(unitFiles, func(left, right int) bool { return unitFiles[left].Path < unitFiles[right].Path })
	seenUnits := make(map[string]struct{}, len(unitFiles))
	for _, file := range unitFiles {
		name := filepath.Base(filepath.FromSlash(file.Path))
		if name != "containerd.service" && name != "kubelet.service" {
			return Report{}, fmt.Errorf("systemd-unit payload %q must be containerd.service or kubelet.service", file.Path)
		}
		if _, exists := seenUnits[name]; exists {
			return Report{}, fmt.Errorf("offline bundle contains duplicate systemd unit %q", name)
		}
		seenUnits[name] = struct{}{}
		unitPath, err := remotePayloadPath(remoteRoot, file.Path)
		if err != nil {
			return Report{}, err
		}
		steps = append(steps, "install -m 0644 -- "+shellQuote(unitPath)+" "+shellQuote("/etc/systemd/system/"+name))
		report.UnitCount++
	}
	if len(seenUnits) != 2 {
		return Report{}, fmt.Errorf("offline bundle must contain containerd.service and kubelet.service, found %d units", len(seenUnits))
	}

	steps = append(steps,
		"systemctl daemon-reload",
		"systemctl enable containerd.service",
		"systemctl restart containerd.service",
		"systemctl enable kubelet.service",
	)
	command := strings.Join(steps, " && ")
	result, err := runner.Run(ctx, command)
	if err != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			return Report{}, fmt.Errorf("prepare remote node: %w; remote stderr: %s", err, stderr)
		}
		return Report{}, fmt.Errorf("prepare remote node: %w", err)
	}
	return report, nil
}

func remotePayloadPath(remoteRoot, payloadPath string) (string, error) {
	if !fs.ValidPath(payloadPath) || strings.Contains(payloadPath, `\`) {
		return "", fmt.Errorf("bundle payload path %q is unsafe", payloadPath)
	}
	joined := path.Join(remoteRoot, payloadPath)
	rootPrefix := strings.TrimSuffix(path.Clean(remoteRoot), "/") + "/"
	if !strings.HasPrefix(joined, rootPrefix) {
		return "", fmt.Errorf("bundle payload path %q escapes remote directory", payloadPath)
	}
	return joined, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
