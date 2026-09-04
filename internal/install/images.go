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
	"path"
	"sort"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
)

// ImageReport 描述一次镜像导入执行的归档数量。
type ImageReport struct {
	KubernetesCount int
	CiliumCount     int
	RegistryCount   int
}

// ImageOptions 控制可选镜像角色是否进入节点。
type ImageOptions struct {
	IncludeRegistry bool
}

// ImportImages 将 Bundle 中的镜像归档导入 containerd 的 k8s.io namespace。
// containerd 必须已经由 PrepareNode 安装并运行；该步骤不会访问镜像仓库。
func ImportImages(ctx context.Context, runner CommandRunner, remoteRoot string, manifest bundle.Manifest, options ImageOptions) (ImageReport, error) {
	if runner == nil {
		return ImageReport{}, fmt.Errorf("image importer command runner is required")
	}
	if !path.IsAbs(remoteRoot) || path.Clean(remoteRoot) == "/" {
		return ImageReport{}, fmt.Errorf("remote bundle directory must be an absolute non-root path")
	}
	if err := manifest.Validate(); err != nil {
		return ImageReport{}, fmt.Errorf("validate offline bundle manifest: %w", err)
	}

	type imageRole struct {
		name     string
		required bool
		add      func(*ImageReport)
	}
	roles := []imageRole{
		{name: "kubernetes-image", required: true, add: func(report *ImageReport) { report.KubernetesCount++ }},
		{name: "cilium-image", required: true, add: func(report *ImageReport) { report.CiliumCount++ }},
	}
	if options.IncludeRegistry {
		roles = append(roles, imageRole{name: "registry-image", required: true, add: func(report *ImageReport) { report.RegistryCount++ }})
	}

	steps := make([]string, 0)
	report := ImageReport{}
	for _, role := range roles {
		files := append([]bundle.File(nil), manifest.FilesForRole(role.name)...)
		if role.required && len(files) == 0 {
			return ImageReport{}, fmt.Errorf("offline bundle must contain at least one %q archive", role.name)
		}
		sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
		for _, file := range files {
			remotePath, err := remotePayloadPath(remoteRoot, file.Path)
			if err != nil {
				return ImageReport{}, err
			}
			steps = append(steps, "ctr -n k8s.io images import --all-platforms "+shellQuote(remotePath))
			role.add(&report)
		}
	}
	if len(steps) == 0 {
		return ImageReport{}, fmt.Errorf("offline bundle does not contain image payloads")
	}

	result, err := runner.Run(ctx, strings.Join(steps, " && "))
	if err != nil {
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			return ImageReport{}, fmt.Errorf("import container images: %w; remote stderr: %s", err, stderr)
		}
		return ImageReport{}, fmt.Errorf("import container images: %w", err)
	}
	return report, nil
}
