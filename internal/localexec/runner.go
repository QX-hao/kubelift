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
package localexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/QX-hao/kubelift/internal/remote"
)

// Runner 在 Master0 本机执行安装器生成的受控 shell 命令。
type Runner struct{}

func (Runner) Run(ctx context.Context, command string) (remote.CommandResult, error) {
	if command == "" {
		return remote.CommandResult{}, fmt.Errorf("local command must not be empty")
	}
	process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	result := remote.CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitError.ExitCode()
		return result, fmt.Errorf("local command exited with code %d: %w", result.ExitCode, err)
	}
	return result, fmt.Errorf("run local command: %w", err)
}
