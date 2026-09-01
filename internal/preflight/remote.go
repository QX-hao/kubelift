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
	"strings"

	"github.com/QX-hao/kubelift/internal/remote"
)

// RemoteRunner 是远程 SSH 客户端需要提供的最小接口，便于独立测试预检逻辑。
type RemoteRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
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
	}
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
