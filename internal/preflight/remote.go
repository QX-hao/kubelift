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
package preflight

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

// RemoteRunner 是远程 SSH 客户端需要提供的最小接口，便于独立测试预检逻辑。
type RemoteRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
}

// CheckRemotePorts 确认目标节点的 Kubernetes 固定监听端口尚未被占用。
func CheckRemotePorts(ctx context.Context, runner RemoteRunner, ports []int) Result {
	result := Result{Name: "Kubernetes ports"}
	commandResult, err := runner.Run(ctx, "ss -H -lnt")
	if err != nil {
		if stderr := strings.TrimSpace(commandResult.Stderr); stderr != "" {
			result.Err = fmt.Errorf("%w; remote stderr: %s", err, stderr)
		} else {
			result.Err = err
		}
		return result
	}
	required := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		required[port] = struct{}{}
	}
	occupied := make(map[int]struct{})
	for _, line := range strings.Split(commandResult.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		separator := strings.LastIndex(fields[3], ":")
		if separator < 0 {
			continue
		}
		port, err := strconv.Atoi(fields[3][separator+1:])
		if err == nil {
			if _, tracked := required[port]; tracked {
				occupied[port] = struct{}{}
			}
		}
	}
	if len(occupied) > 0 {
		values := make([]int, 0, len(occupied))
		for port := range occupied {
			values = append(values, port)
		}
		sort.Ints(values)
		result.Err = fmt.Errorf("required Kubernetes ports are already in use: %v", values)
		return result
	}
	result.Detail = fmt.Sprintf("available: %v", ports)
	return result
}

const (
	minimumCPUCount        = 2
	minimumMemoryKB uint64 = 1800 * 1024
	minimumDiskKB   uint64 = 10 * 1024 * 1024
)

// CheckRemoteBundle 确认远程节点的平台与离线 Bundle 清单完全兼容。
func CheckRemoteBundle(ctx context.Context, runner RemoteRunner, manifest bundle.Manifest) []Result {
	return []Result{
		checkRemoteCommand(ctx, runner, "bundle architecture", "uname -m", func(output string) (string, error) {
			expected, supported := supportedArchitectures[manifest.Spec.Architecture]
			if !supported {
				return "", fmt.Errorf("bundle uses unsupported architecture %q", manifest.Spec.Architecture)
			}
			actual := strings.TrimSpace(output)
			if actual != expected {
				return "", fmt.Errorf("bundle architecture %s requires %s, detected %s", manifest.Spec.Architecture, expected, actual)
			}
			return manifest.Spec.Architecture + " (" + actual + ")", nil
		}),
		checkRemoteCommand(ctx, runner, "bundle Ubuntu version", "cat /etc/os-release", func(output string) (string, error) {
			values, err := parseOSRelease(strings.NewReader(output))
			if err != nil {
				return "", err
			}
			if values["ID"] != "ubuntu" {
				return "", fmt.Errorf("bundle requires Ubuntu, detected %q", values["ID"])
			}
			actual := values["VERSION_ID"]
			for _, supported := range manifest.Spec.UbuntuVersions {
				if actual == supported {
					return "Ubuntu " + actual, nil
				}
			}
			return "", fmt.Errorf("bundle supports Ubuntu %s, detected %s", strings.Join(manifest.Spec.UbuntuVersions, ", "), actual)
		}),
	}
}

// CheckRemote 只读检查远程节点是否满足 KubeLift 的基础系统要求。
func CheckRemote(ctx context.Context, runner RemoteRunner) []Result {
	return []Result{
		checkRemoteCommand(ctx, runner, "hostname", "hostname", func(output string) (string, error) {
			hostname := strings.TrimSpace(output)
			if hostname == "" {
				return "", fmt.Errorf("remote hostname is empty")
			}
			return hostname, nil
		}),
		checkRemoteCommand(ctx, runner, "architecture", "uname -m", func(output string) (string, error) {
			machineArchitecture := strings.TrimSpace(output)
			for architecture, uname := range supportedArchitectures {
				if uname == machineArchitecture {
					return fmt.Sprintf("%s (%s)", architecture, machineArchitecture), nil
				}
			}
			return "", fmt.Errorf("unsupported architecture %q", machineArchitecture)
		}),
		checkRemoteCommand(ctx, runner, "operating system", "cat /etc/os-release", func(output string) (string, error) {
			values, err := parseOSRelease(strings.NewReader(output))
			if err != nil {
				return "", err
			}
			if values["ID"] != "ubuntu" {
				return "", fmt.Errorf("requires Ubuntu, detected %q", values["ID"])
			}
			version := values["VERSION_ID"]
			if _, supported := supportedUbuntuVersions[version]; !supported {
				return "", fmt.Errorf("unsupported Ubuntu version %q", version)
			}
			return "Ubuntu " + version, nil
		}),
		checkRemoteCommand(ctx, runner, "swap", "swapon --noheadings --show", func(output string) (string, error) {
			if strings.TrimSpace(output) != "" {
				return "", fmt.Errorf("swap is enabled: %s", strings.TrimSpace(output))
			}
			return "disabled", nil
		}),
		checkRemoteUint(ctx, runner, "CPU", "getconf _NPROCESSORS_ONLN", minimumCPUCount, "logical processors"),
		checkRemoteUint(ctx, runner, "memory", "awk '/MemTotal:/ {print $2}' /proc/meminfo", minimumMemoryKB, "KiB"),
		checkRemoteUint(ctx, runner, "disk", "df -Pk /var/lib | awk 'NR==2 {print $4}'", minimumDiskKB, "KiB available under /var/lib"),
		checkRemoteCommand(ctx, runner, "systemd", "test -d /run/systemd/system && printf systemd", func(output string) (string, error) {
			if strings.TrimSpace(output) != "systemd" {
				return "", fmt.Errorf("systemd is not running")
			}
			return "running", nil
		}),
	}
}

func checkRemoteUint(ctx context.Context, runner RemoteRunner, name, command string, minimum uint64, unit string) Result {
	return checkRemoteCommand(ctx, runner, name, command, func(output string) (string, error) {
		value, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid numeric value %q", strings.TrimSpace(output))
		}
		if value < minimum {
			return "", fmt.Errorf("requires at least %d %s, detected %d", minimum, unit, value)
		}
		return fmt.Sprintf("%d %s", value, unit), nil
	})
}

func checkRemoteCommand(ctx context.Context, runner RemoteRunner, name, command string, parse func(string) (string, error)) Result {
	result := Result{Name: name}
	commandResult, err := runner.Run(ctx, command)
	if err != nil {
		if stderr := strings.TrimSpace(commandResult.Stderr); stderr != "" {
			result.Err = fmt.Errorf("%w; remote stderr: %s", err, stderr)
		} else {
			result.Err = err
		}
		return result
	}
	result.Detail, result.Err = parse(commandResult.Stdout)
	return result
}
