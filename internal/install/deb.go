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
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

// CommandRunner 是远程安装步骤需要的最小接口。
type CommandRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
}

// InstallDebPackages 使用已上传到 staging 目录的 Debian 包离线安装组件。
// 先统一解包，再让 dpkg 按依赖关系配置，整个过程不会访问网络。
func InstallDebPackages(ctx context.Context, runner CommandRunner, remoteRoot string, files []bundle.File) (int, error) {
	if runner == nil {
		return 0, fmt.Errorf("package installer command runner is required")
	}
	if !path.IsAbs(remoteRoot) || path.Clean(remoteRoot) == "/" {
		return 0, fmt.Errorf("remote package directory must be an absolute non-root path")
	}

	packagePaths := make([]string, 0)
	for _, file := range files {
		if file.Kind != "package" {
			continue
		}
		if !fs.ValidPath(file.Path) || strings.Contains(file.Path, `\`) {
			return 0, fmt.Errorf("package payload path %q is unsafe", file.Path)
		}
		packagePath := path.Join(remoteRoot, file.Path)
		rootPrefix := strings.TrimSuffix(path.Clean(remoteRoot), "/") + "/"
		if !strings.HasPrefix(packagePath, rootPrefix) {
			return 0, fmt.Errorf("package payload path %q escapes remote directory", file.Path)
		}
		packagePaths = append(packagePaths, packagePath)
	}
	if len(packagePaths) == 0 {
		return 0, fmt.Errorf("offline bundle does not contain package payloads")
	}
	sort.Strings(packagePaths)

	quotedPaths := make([]string, len(packagePaths))
	for index, packagePath := range packagePaths {
		quotedPaths[index] = shellQuote(packagePath)
	}
	command := "dpkg --unpack -- " + strings.Join(quotedPaths, " ") + " && dpkg --configure --pending"
	result, err := runner.Run(ctx, command)
	if err != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			return 0, fmt.Errorf("install Debian packages: %w; remote stderr: %s", err, stderr)
		}
		return 0, fmt.Errorf("install Debian packages: %w", err)
	}
	return len(packagePaths), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
