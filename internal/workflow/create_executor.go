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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/install"
	"github.com/QX-hao/kubelift/internal/kubeadm"
	"github.com/QX-hao/kubelift/internal/localexec"
)

const (
	defaultStagingRoot     = "/var/lib/kubelift/staging"
	defaultKubeadmConfig   = "/etc/kubernetes/kubelift-init.yaml"
	defaultAdminKubeconfig = "/etc/kubernetes/admin.conf"
)

// CreateExecutor 描述 Master0 本地集群初始化需要的依赖和受管路径。
type CreateExecutor struct {
	Runner          install.CommandRunner
	Transport       distribute.FileTransport
	StagingRoot     string
	KubeadmConfig   string
	AdminKubeconfig string
	EffectiveUserID func() int
}

// CreateResult 汇总 Master0 初始化阶段完成的工作。
type CreateResult struct {
	PayloadCount  int
	Preparation   install.Report
	Images        install.ImageReport
	KubeadmOutput string
}

// NewLocalCreateExecutor 返回只操作 KubeLift 受管路径的本地执行器。
func NewLocalCreateExecutor() CreateExecutor {
	return CreateExecutor{
		Runner:          localexec.Runner{},
		Transport:       distribute.LocalTransport{},
		StagingRoot:     defaultStagingRoot,
		KubeadmConfig:   defaultKubeadmConfig,
		AdminKubeconfig: defaultAdminKubeconfig,
		EffectiveUserID: os.Geteuid,
	}
}

// ExecuteCreate 在 Master0 上准备离线载荷并执行 kubeadm init。
// 已存在 admin.conf 时立即拒绝，避免覆盖或重复初始化现有集群。
func (e CreateExecutor) ExecuteCreate(ctx context.Context, configuration config.Config) (CreateResult, error) {
	if err := configuration.Validate(); err != nil {
		return CreateResult{}, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if e.Runner == nil || e.Transport == nil || e.EffectiveUserID == nil {
		return CreateResult{}, fmt.Errorf("create executor dependencies are incomplete")
	}
	for name, value := range map[string]string{
		"staging root":     e.StagingRoot,
		"kubeadm config":   e.KubeadmConfig,
		"admin kubeconfig": e.AdminKubeconfig,
	} {
		if !filepath.IsAbs(value) || filepath.Clean(value) == string(filepath.Separator) {
			return CreateResult{}, fmt.Errorf("%s must be an absolute non-root path", name)
		}
	}
	if e.EffectiveUserID() != 0 {
		return CreateResult{}, fmt.Errorf("cluster creation must run as root")
	}
	if _, err := os.Stat(e.AdminKubeconfig); err == nil {
		return CreateResult{}, fmt.Errorf("Kubernetes admin kubeconfig already exists at %q", e.AdminKubeconfig)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, fmt.Errorf("check existing Kubernetes admin kubeconfig: %w", err)
	}

	remoteRoot := filepath.Join(e.StagingRoot, configuration.Metadata.Name)
	staged, err := distribute.Push(ctx, e.Transport, configuration.Spec.Offline.Bundle, remoteRoot)
	if err != nil {
		return CreateResult{}, fmt.Errorf("stage Master0 bundle: %w", err)
	}
	preparation, err := install.PrepareNode(ctx, e.Runner, staged.RemoteRoot, staged.Manifest)
	if err != nil {
		return CreateResult{}, err
	}
	images, err := install.ImportImages(ctx, e.Runner, staged.RemoteRoot, staged.Manifest)
	if err != nil {
		return CreateResult{}, err
	}
	kubeadmConfig, err := kubeadm.GenerateInitConfig(configuration)
	if err != nil {
		return CreateResult{}, err
	}
	if err := writePrivateFile(e.KubeadmConfig, kubeadmConfig); err != nil {
		return CreateResult{}, err
	}

	command := "/usr/bin/kubeadm init --config " + quoteShell(e.KubeadmConfig)
	commandResult, err := e.Runner.Run(ctx, command)
	if err != nil {
		if stderr := strings.TrimSpace(commandResult.Stderr); stderr != "" {
			return CreateResult{}, fmt.Errorf("initialize Kubernetes control plane: %w; stderr: %s", err, stderr)
		}
		return CreateResult{}, fmt.Errorf("initialize Kubernetes control plane: %w", err)
	}
	return CreateResult{
		PayloadCount:  len(staged.Files),
		Preparation:   preparation,
		Images:        images,
		KubeadmOutput: strings.TrimSpace(commandResult.Stdout),
	}, nil
}

func writePrivateFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create kubeadm configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kubelift-kubeadm-")
	if err != nil {
		return fmt.Errorf("create temporary kubeadm configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set kubeadm configuration permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write kubeadm configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close kubeadm configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace kubeadm configuration %q: %w", path, err)
	}
	return nil
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
