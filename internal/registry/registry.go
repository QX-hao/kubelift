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
package registry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/remote"
	"gopkg.in/yaml.v3"
)

const (
	registryPortPlaceholder        = "{{ .RegistryPort }}"
	registryStoragePathPlaceholder = "{{ .RegistryStoragePath }}"
	registryLabel                  = "app.kubernetes.io/name=kubelift-registry"
)

// CommandRunner 是 Registry 健康检查需要的最小命令接口。
type CommandRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
}

type templateData struct {
	RegistryPort        int
	RegistryStoragePath string
}

type staticPod struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		HostNetwork bool `yaml:"hostNetwork"`
		Containers  []struct {
			ImagePullPolicy string `yaml:"imagePullPolicy"`
			Ports           []struct {
				ContainerPort int `yaml:"containerPort"`
			} `yaml:"ports"`
			ReadinessProbe *struct {
				HTTPGet *struct {
					Path string `yaml:"path"`
					Port int    `yaml:"port"`
				} `yaml:"httpGet"`
			} `yaml:"readinessProbe"`
		} `yaml:"containers"`
		Volumes []struct {
			HostPath *struct {
				Path string `yaml:"path"`
			} `yaml:"hostPath"`
			PersistentVolumeClaim any `yaml:"persistentVolumeClaim"`
		} `yaml:"volumes"`
	} `yaml:"spec"`
}

// RenderManifest 渲染并校验 Registry hostNetwork 静态 Pod 模板。
func RenderManifest(configuration config.Config, storagePath string, source []byte) ([]byte, error) {
	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if !configuration.Spec.Registry.Enabled {
		return nil, fmt.Errorf("Registry is disabled in cluster configuration")
	}
	if !filepath.IsAbs(storagePath) || filepath.Clean(storagePath) == string(filepath.Separator) {
		return nil, fmt.Errorf("Registry storage path must be an absolute non-root path")
	}
	templateText := string(source)
	if !strings.Contains(templateText, registryPortPlaceholder) || !strings.Contains(templateText, registryStoragePathPlaceholder) {
		return nil, fmt.Errorf("Registry manifest template must contain %q and %q", registryPortPlaceholder, registryStoragePathPlaceholder)
	}
	manifestTemplate, err := template.New("registry").Option("missingkey=error").Parse(templateText)
	if err != nil {
		return nil, fmt.Errorf("parse Registry manifest template: %w", err)
	}
	var output bytes.Buffer
	if err := manifestTemplate.Execute(&output, templateData{
		RegistryPort:        configuration.Spec.Registry.Port,
		RegistryStoragePath: storagePath,
	}); err != nil {
		return nil, fmt.Errorf("render Registry manifest template: %w", err)
	}
	if err := validateStaticPod(output.Bytes(), configuration.Spec.Registry.Port, storagePath); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// WaitReady 等待静态 Pod 的 mirror Pod 进入 Ready 状态。
func WaitReady(ctx context.Context, runner CommandRunner, kubeconfigPath string) error {
	if runner == nil {
		return fmt.Errorf("Registry command runner is required")
	}
	if !filepath.IsAbs(kubeconfigPath) {
		return fmt.Errorf("Registry kubeconfig path must be absolute")
	}
	command := "/usr/bin/kubectl --kubeconfig " + shellQuote(kubeconfigPath) +
		" -n kube-system wait --for=condition=Ready pod -l " + shellQuote(registryLabel) + " --timeout=5m"
	result, err := runner.Run(ctx, command)
	if err == nil {
		return nil
	}
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		return fmt.Errorf("wait for Registry cache: %w; stderr: %s", err, stderr)
	}
	return fmt.Errorf("wait for Registry cache: %w", err)
}

func validateStaticPod(contents []byte, port int, storagePath string) error {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var pod staticPod
	if err := decoder.Decode(&pod); err != nil {
		return fmt.Errorf("decode rendered Registry manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("decode rendered Registry manifest: %w", err)
		}
		return fmt.Errorf("Registry manifest must contain exactly one YAML document")
	}
	if pod.Kind != "Pod" || pod.Metadata.Name != "kubelift-registry" || pod.Metadata.Namespace != "kube-system" {
		return fmt.Errorf("Registry manifest must define Pod kube-system/kubelift-registry")
	}
	if pod.Metadata.Labels["app.kubernetes.io/name"] != "kubelift-registry" {
		return fmt.Errorf("Registry Pod must have label %q", registryLabel)
	}
	if !pod.Spec.HostNetwork {
		return fmt.Errorf("Registry Pod must use hostNetwork")
	}
	if len(pod.Spec.Containers) == 0 {
		return fmt.Errorf("Registry Pod must contain at least one container")
	}
	portFound := false
	readinessFound := false
	for _, container := range pod.Spec.Containers {
		if container.ImagePullPolicy != "Never" {
			return fmt.Errorf("Registry containers must use imagePullPolicy Never")
		}
		for _, containerPort := range container.Ports {
			if containerPort.ContainerPort == port {
				portFound = true
			}
		}
		if container.ReadinessProbe != nil && container.ReadinessProbe.HTTPGet != nil &&
			container.ReadinessProbe.HTTPGet.Path == "/v2/" && container.ReadinessProbe.HTTPGet.Port == port {
			readinessFound = true
		}
	}
	if !portFound {
		return fmt.Errorf("Registry Pod must expose configured port %d", port)
	}
	if !readinessFound {
		return fmt.Errorf("Registry Pod must probe HTTP path /v2/ on configured port %d", port)
	}
	hostPathFound := false
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			return fmt.Errorf("Registry Pod must not depend on a persistent volume claim")
		}
		if volume.HostPath != nil && volume.HostPath.Path == storagePath {
			hostPathFound = true
		}
	}
	if !hostPathFound {
		return fmt.Errorf("Registry Pod must use hostPath %q", storagePath)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
