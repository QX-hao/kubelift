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
package cilium

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/remote"
	"gopkg.in/yaml.v3"
)

const (
	apiServerHostPlaceholder = "{{ .APIServerHost }}"
	apiServerPortPlaceholder = "{{ .APIServerPort }}"
)

// CommandRunner 是 Cilium 安装和健康检查需要的最小命令接口。
type CommandRunner interface {
	Run(context.Context, string) (remote.CommandResult, error)
}

type templateData struct {
	APIServerHost string
	APIServerPort int
	PodCIDR       string
	ClusterName   string
}

// RenderManifest 将集群相关值写入离线 Bundle 中的 Cilium 模板。
func RenderManifest(configuration config.Config, source []byte) ([]byte, error) {
	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster configuration: %w", err)
	}
	templateText := string(source)
	if !strings.Contains(templateText, apiServerHostPlaceholder) || !strings.Contains(templateText, apiServerPortPlaceholder) {
		return nil, fmt.Errorf("Cilium manifest template must contain %q and %q", apiServerHostPlaceholder, apiServerPortPlaceholder)
	}
	host := configuration.Spec.ControlPlane.AdvertiseAddress
	port := 6443
	if endpoint := configuration.Spec.ControlPlane.Endpoint; endpoint != "" {
		var portText string
		var err error
		host, portText, err = net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse control-plane endpoint: %w", err)
		}
		port, err = strconv.Atoi(portText)
		if err != nil {
			return nil, fmt.Errorf("parse control-plane endpoint port: %w", err)
		}
	}

	manifestTemplate, err := template.New("cilium").Option("missingkey=error").Parse(templateText)
	if err != nil {
		return nil, fmt.Errorf("parse Cilium manifest template: %w", err)
	}
	var output bytes.Buffer
	if err := manifestTemplate.Execute(&output, templateData{
		APIServerHost: host,
		APIServerPort: port,
		PodCIDR:       configuration.Spec.Network.PodCIDR,
		ClusterName:   configuration.Metadata.Name,
	}); err != nil {
		return nil, fmt.Errorf("render Cilium manifest template: %w", err)
	}
	if err := validateYAML(output.Bytes()); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// InstallAndWait 应用 Cilium 清单，并等待网络、节点和 CoreDNS 就绪。
func InstallAndWait(ctx context.Context, runner CommandRunner, manifestPath, kubeconfigPath string) error {
	if runner == nil {
		return fmt.Errorf("Cilium command runner is required")
	}
	if !filepath.IsAbs(manifestPath) || !filepath.IsAbs(kubeconfigPath) {
		return fmt.Errorf("Cilium manifest and kubeconfig paths must be absolute")
	}
	kubectl := "/usr/bin/kubectl --kubeconfig " + shellQuote(kubeconfigPath)
	stages := []struct {
		name    string
		command string
	}{
		{name: "check API server", command: kubectl + " get --raw=/readyz"},
		{name: "apply Cilium manifest", command: kubectl + " apply -f " + shellQuote(manifestPath)},
		{name: "wait for Cilium agents", command: kubectl + " -n kube-system rollout status daemonset/cilium --timeout=10m"},
		{name: "wait for Cilium operator", command: kubectl + " -n kube-system rollout status deployment/cilium-operator --timeout=10m"},
		{name: "wait for nodes", command: kubectl + " wait --for=condition=Ready nodes --all --timeout=10m"},
		{name: "wait for CoreDNS", command: kubectl + " -n kube-system rollout status deployment/coredns --timeout=10m"},
	}
	for _, stage := range stages {
		result, err := runner.Run(ctx, stage.command)
		if err == nil {
			continue
		}
		if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
			return fmt.Errorf("%s: %w; stderr: %s", stage.name, err, stderr)
		}
		return fmt.Errorf("%s: %w", stage.name, err)
	}
	return nil
}

func validateYAML(contents []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	documents := 0
	for {
		var document any
		err := decoder.Decode(&document)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decode rendered Cilium manifest: %w", err)
		}
		if document != nil {
			documents++
		}
	}
	if documents == 0 {
		return fmt.Errorf("rendered Cilium manifest must contain at least one YAML document")
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
