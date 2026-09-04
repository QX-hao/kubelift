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

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/cilium"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/install"
	"github.com/QX-hao/kubelift/internal/kubeadm"
	"github.com/QX-hao/kubelift/internal/localexec"
	"github.com/QX-hao/kubelift/internal/registry"
	"github.com/QX-hao/kubelift/internal/remote"
)

const (
	defaultStagingRoot      = "/var/lib/kubelift/staging"
	defaultKubeadmConfig    = "/etc/kubernetes/kubelift-init.yaml"
	defaultCiliumManifest   = "/etc/kubernetes/kubelift-cilium.yaml"
	defaultRegistryManifest = "/etc/kubernetes/manifests/kubelift-registry.yaml"
	defaultRegistryStorage  = "/var/lib/kubelift/registry"
	defaultAdminKubeconfig  = "/etc/kubernetes/admin.conf"
	defaultCreateStateRoot  = "/var/lib/kubelift/state"
)

// CreateExecutor 描述 Master0 本地集群初始化需要的依赖和受管路径。
type CreateExecutor struct {
	Runner           install.CommandRunner
	Transport        distribute.FileTransport
	StagingRoot      string
	KubeadmConfig    string
	CiliumManifest   string
	RegistryManifest string
	RegistryStorage  string
	AdminKubeconfig  string
	StateRoot        string
	EffectiveUserID  func() int
}

// CreateOptions 控制首次创建或从已记录阶段继续执行。
type CreateOptions struct {
	Resume bool
}

// CreateResult 汇总 Master0 初始化阶段完成的工作。
type CreateResult struct {
	PayloadCount    int
	Preparation     install.Report
	Images          install.ImageReport
	KubeadmOutput   string
	RegistryReady   bool
	Phase           CreatePhase
	AlreadyComplete bool
}

// NewLocalCreateExecutor 返回只操作 KubeLift 受管路径的本地执行器。
func NewLocalCreateExecutor() CreateExecutor {
	return CreateExecutor{
		Runner:           localexec.Runner{},
		Transport:        distribute.LocalTransport{},
		StagingRoot:      defaultStagingRoot,
		KubeadmConfig:    defaultKubeadmConfig,
		CiliumManifest:   defaultCiliumManifest,
		RegistryManifest: defaultRegistryManifest,
		RegistryStorage:  defaultRegistryStorage,
		AdminKubeconfig:  defaultAdminKubeconfig,
		StateRoot:        defaultCreateStateRoot,
		EffectiveUserID:  os.Geteuid,
	}
}

// ExecuteCreate 在 Master0 上准备离线载荷并执行 kubeadm init。
// 恢复时只跳过已明确完成的阶段，不自动清理 kubeadm 留下的现场。
func (e CreateExecutor) ExecuteCreate(ctx context.Context, configuration config.Config, options CreateOptions) (CreateResult, error) {
	if err := configuration.Validate(); err != nil {
		return CreateResult{}, fmt.Errorf("validate cluster configuration: %w", err)
	}
	if e.Runner == nil || e.Transport == nil || e.EffectiveUserID == nil {
		return CreateResult{}, fmt.Errorf("create executor dependencies are incomplete")
	}
	managedPaths := map[string]string{
		"staging root":     e.StagingRoot,
		"kubeadm config":   e.KubeadmConfig,
		"Cilium manifest":  e.CiliumManifest,
		"admin kubeconfig": e.AdminKubeconfig,
		"state root":       e.StateRoot,
	}
	if configuration.Spec.Registry.Enabled {
		managedPaths["Registry manifest"] = e.RegistryManifest
		managedPaths["Registry storage"] = e.RegistryStorage
	}
	for name, value := range managedPaths {
		if !filepath.IsAbs(value) || filepath.Clean(value) == string(filepath.Separator) {
			return CreateResult{}, fmt.Errorf("%s must be an absolute non-root path", name)
		}
	}
	if e.EffectiveUserID() != 0 {
		return CreateResult{}, fmt.Errorf("cluster creation must run as root")
	}
	adminExists, err := pathExists(e.AdminKubeconfig)
	if err != nil {
		return CreateResult{}, fmt.Errorf("check existing Kubernetes admin kubeconfig: %w", err)
	}
	configHash, err := configurationHash(configuration)
	if err != nil {
		return CreateResult{}, err
	}
	bundleHash, err := fileSHA256(configuration.Spec.Offline.Bundle)
	if err != nil {
		return CreateResult{}, err
	}
	statePath := createStatePath(e.StateRoot, configuration.Metadata.Name)
	state, stateErr := loadCreateState(statePath)
	stateExists := stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return CreateResult{}, fmt.Errorf("load create state %q: %w", statePath, stateErr)
	}
	if !options.Resume {
		if stateExists {
			return CreateResult{}, fmt.Errorf("create state already exists at %q; use --resume to continue", statePath)
		}
		if adminExists {
			return CreateResult{}, fmt.Errorf("Kubernetes admin kubeconfig already exists at %q", e.AdminKubeconfig)
		}
		state = newCreateState(configuration, configHash, bundleHash)
	} else {
		if !stateExists {
			return CreateResult{}, fmt.Errorf("no create state exists at %q; cannot resume", statePath)
		}
		if state.Cluster != configuration.Metadata.Name || state.KubernetesVersion != configuration.Spec.Kubernetes.Version ||
			state.ConfigurationSHA256 != configHash || state.BundleSHA256 != bundleHash {
			return CreateResult{}, fmt.Errorf("create state does not match the current cluster configuration and offline bundle")
		}
		if state.Phase == PhaseComplete {
			return CreateResult{Phase: PhaseComplete, AlreadyComplete: true, RegistryReady: configuration.Spec.Registry.Enabled}, nil
		}
	}
	if state.reached(PhaseKubeadmInitialized) && !adminExists {
		return CreateResult{}, fmt.Errorf("create state is %q but admin kubeconfig is missing at %q", state.Phase, e.AdminKubeconfig)
	}
	if !state.reached(PhaseKubeadmStarting) && adminExists {
		return CreateResult{}, fmt.Errorf("admin kubeconfig exists but create state has only reached %q", state.Phase)
	}
	if state.Phase == PhaseKubeadmStarting && !adminExists {
		return CreateResult{}, fmt.Errorf("kubeadm initialization may be incomplete; inspect the host before retrying and do not run kubeadm reset automatically")
	}

	remoteRoot := filepath.Join(e.StagingRoot, configuration.Metadata.Name)
	staged, err := distribute.Push(ctx, e.Transport, configuration.Spec.Offline.Bundle, remoteRoot)
	if err != nil {
		return CreateResult{}, fmt.Errorf("stage Master0 bundle: %w", err)
	}
	if err := bundle.ValidateClusterProfile(staged.Manifest, configuration.Spec.Registry.Enabled); err != nil {
		return CreateResult{}, fmt.Errorf("validate cluster bundle profile: %w", err)
	}
	if !state.reached(PhaseStaged) {
		if err := saveCreateState(statePath, state, PhaseStaged); err != nil {
			return CreateResult{}, err
		}
		state.Phase = PhaseStaged
	}
	ciliumFiles := staged.Manifest.FilesForRole("cilium-manifest")
	if len(ciliumFiles) != 1 {
		return CreateResult{}, fmt.Errorf("offline bundle must contain exactly one Cilium manifest template, found %d", len(ciliumFiles))
	}
	ciliumTemplatePath := filepath.Join(staged.RemoteRoot, filepath.FromSlash(ciliumFiles[0].Path))
	ciliumTemplate, err := os.ReadFile(ciliumTemplatePath)
	if err != nil {
		return CreateResult{}, fmt.Errorf("read staged Cilium manifest template: %w", err)
	}
	renderedCilium, err := cilium.RenderManifest(configuration, ciliumTemplate)
	if err != nil {
		return CreateResult{}, err
	}
	var renderedRegistry []byte
	if configuration.Spec.Registry.Enabled {
		if registryImages := staged.Manifest.FilesForRole("registry-image"); len(registryImages) == 0 {
			return CreateResult{}, fmt.Errorf("offline bundle must contain at least one Registry image archive")
		}
		registryFiles := staged.Manifest.FilesForRole("registry-manifest")
		if len(registryFiles) != 1 {
			return CreateResult{}, fmt.Errorf("offline bundle must contain exactly one Registry manifest template, found %d", len(registryFiles))
		}
		registryTemplatePath := filepath.Join(staged.RemoteRoot, filepath.FromSlash(registryFiles[0].Path))
		registryTemplate, err := os.ReadFile(registryTemplatePath)
		if err != nil {
			return CreateResult{}, fmt.Errorf("read staged Registry manifest template: %w", err)
		}
		renderedRegistry, err = registry.RenderManifest(configuration, e.RegistryStorage, registryTemplate)
		if err != nil {
			return CreateResult{}, err
		}
	}
	kubeadmConfig, err := kubeadm.GenerateInitConfig(configuration)
	if err != nil {
		return CreateResult{}, err
	}
	var preparation install.Report
	if !state.reached(PhasePrepared) {
		preparation, err = install.PrepareNode(ctx, e.Runner, staged.RemoteRoot, staged.Manifest)
		if err != nil {
			return CreateResult{}, err
		}
		if err := saveCreateState(statePath, state, PhasePrepared); err != nil {
			return CreateResult{}, err
		}
		state.Phase = PhasePrepared
	}
	var images install.ImageReport
	if !state.reached(PhaseImagesImported) {
		images, err = install.ImportImages(ctx, e.Runner, staged.RemoteRoot, staged.Manifest, install.ImageOptions{
			IncludeRegistry: configuration.Spec.Registry.Enabled,
		})
		if err != nil {
			return CreateResult{}, err
		}
		if err := saveCreateState(statePath, state, PhaseImagesImported); err != nil {
			return CreateResult{}, err
		}
		state.Phase = PhaseImagesImported
	}
	if err := writePrivateFile(e.KubeadmConfig, kubeadmConfig); err != nil {
		return CreateResult{}, err
	}
	if err := writePrivateFile(e.CiliumManifest, renderedCilium); err != nil {
		return CreateResult{}, err
	}

	var commandResult remote.CommandResult
	if !state.reached(PhaseKubeadmInitialized) {
		if state.Phase == PhaseKubeadmStarting && adminExists {
			state.Phase = PhaseKubeadmInitialized
			if err := saveCreateState(statePath, state, PhaseKubeadmInitialized); err != nil {
				return CreateResult{}, err
			}
		} else {
			if err := saveCreateState(statePath, state, PhaseKubeadmStarting); err != nil {
				return CreateResult{}, err
			}
			state.Phase = PhaseKubeadmStarting
			// 首版固定由 Cilium 完全替代 kube-proxy，因此不要求主机安装 conntrack CLI。
			command := "/usr/bin/kubeadm init --ignore-preflight-errors=FileExisting-conntrack --config " + quoteShell(e.KubeadmConfig)
			commandResult, err = e.Runner.Run(ctx, command)
			if err != nil {
				if stderr := strings.TrimSpace(commandResult.Stderr); stderr != "" {
					return CreateResult{}, fmt.Errorf("initialize Kubernetes control plane: %w; stderr: %s", err, stderr)
				}
				return CreateResult{}, fmt.Errorf("initialize Kubernetes control plane: %w", err)
			}
			adminExists, err = pathExists(e.AdminKubeconfig)
			if err != nil {
				return CreateResult{}, fmt.Errorf("check generated Kubernetes admin kubeconfig: %w", err)
			}
			if !adminExists {
				return CreateResult{}, fmt.Errorf("kubeadm init succeeded but admin kubeconfig was not created at %q", e.AdminKubeconfig)
			}
			if err := saveCreateState(statePath, state, PhaseKubeadmInitialized); err != nil {
				return CreateResult{}, err
			}
			state.Phase = PhaseKubeadmInitialized
		}
	}
	if !state.reached(PhaseCiliumReady) {
		if err := cilium.InstallAndWait(ctx, e.Runner, e.CiliumManifest, e.AdminKubeconfig); err != nil {
			return CreateResult{}, err
		}
		if err := saveCreateState(statePath, state, PhaseCiliumReady); err != nil {
			return CreateResult{}, err
		}
		state.Phase = PhaseCiliumReady
	}
	registryReady := configuration.Spec.Registry.Enabled && state.reached(PhaseRegistryReady)
	if configuration.Spec.Registry.Enabled && !state.reached(PhaseRegistryReady) {
		if err := os.MkdirAll(e.RegistryStorage, 0o700); err != nil {
			return CreateResult{}, fmt.Errorf("create Registry storage directory: %w", err)
		}
		if err := writePrivateFile(e.RegistryManifest, renderedRegistry); err != nil {
			return CreateResult{}, err
		}
		if err := registry.WaitReady(ctx, e.Runner, e.AdminKubeconfig); err != nil {
			return CreateResult{}, err
		}
		registryReady = true
		if err := saveCreateState(statePath, state, PhaseRegistryReady); err != nil {
			return CreateResult{}, err
		}
		state.Phase = PhaseRegistryReady
	}
	if err := saveCreateState(statePath, state, PhaseComplete); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		PayloadCount:  len(staged.Files),
		Preparation:   preparation,
		Images:        images,
		KubeadmOutput: strings.TrimSpace(commandResult.Stdout),
		RegistryReady: registryReady,
		Phase:         PhaseComplete,
	}, nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writePrivateFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create managed file directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kubelift-")
	if err != nil {
		return fmt.Errorf("create temporary managed file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set managed file permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write managed file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close managed file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace managed file %q: %w", path, err)
	}
	return nil
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
