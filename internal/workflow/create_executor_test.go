package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/distribute"
	"github.com/QX-hao/kubelift/internal/remote"
	"gopkg.in/yaml.v3"
)

type createRunner struct {
	commands        []string
	adminKubeconfig string
}

func (r *createRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	if strings.HasPrefix(command, "/usr/bin/kubeadm init") {
		if err := os.MkdirAll(filepath.Dir(r.adminKubeconfig), 0o700); err != nil {
			return remote.CommandResult{}, err
		}
		if err := os.WriteFile(r.adminKubeconfig, []byte("created"), 0o600); err != nil {
			return remote.CommandResult{}, err
		}
		return remote.CommandResult{Stdout: "control plane initialized\n"}, nil
	}
	return remote.CommandResult{}, nil
}

func TestCreateExecutorStagesPreparesImportsAndInitializes(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	adminKubeconfig := filepath.Join(root, "kubernetes", "admin.conf")
	runner := &createRunner{adminKubeconfig: adminKubeconfig}
	executor := CreateExecutor{
		Runner:           runner,
		Transport:        distribute.LocalTransport{},
		StagingRoot:      filepath.Join(root, "staging"),
		KubeadmConfig:    filepath.Join(root, "kubernetes", "kubelift-init.yaml"),
		CiliumManifest:   filepath.Join(root, "kubernetes", "kubelift-cilium.yaml"),
		RegistryManifest: filepath.Join(root, "kubernetes", "manifests", "kubelift-registry.yaml"),
		RegistryStorage:  filepath.Join(root, "registry"),
		AdminKubeconfig:  adminKubeconfig,
		StateRoot:        filepath.Join(root, "state"),
		EffectiveUserID:  func() int { return 0 },
	}

	result, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{})
	if err != nil {
		t.Fatalf("ExecuteCreate() error = %v", err)
	}
	if result.PayloadCount != 13 || result.Images.KubernetesCount != 1 || result.Images.CiliumCount != 1 || result.Images.RegistryCount != 1 || !result.RegistryReady {
		t.Fatalf("create result = %+v", result)
	}
	if result.KubeadmOutput != "control plane initialized" {
		t.Fatalf("kubeadm output = %q", result.KubeadmOutput)
	}
	state, err := loadCreateState(createStatePath(executor.StateRoot, configuration.Metadata.Name))
	if err != nil || state.Phase != PhaseComplete {
		t.Fatalf("create state = %+v, error = %v", state, err)
	}
	commandCount := len(runner.commands)
	if _, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{}); err == nil || !strings.Contains(err.Error(), "use --resume") {
		t.Fatalf("second create error = %v, want explicit resume error", err)
	}
	resumed, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{Resume: true})
	if err != nil || !resumed.AlreadyComplete || len(runner.commands) != commandCount {
		t.Fatalf("completed resume = %+v, error = %v, commands = %v", resumed, err, runner.commands)
	}
	if len(runner.commands) != 10 {
		t.Fatalf("command count = %d, want 10: %v", len(runner.commands), runner.commands)
	}
	if !strings.Contains(runner.commands[0], "systemctl restart containerd.service") {
		t.Fatalf("first command does not prepare node: %s", runner.commands[0])
	}
	if !strings.Contains(runner.commands[1], "ctr -n k8s.io images import") {
		t.Fatalf("second command does not import images: %s", runner.commands[1])
	}
	if runner.commands[2] != "/usr/bin/kubeadm init --ignore-preflight-errors=FileExisting-conntrack --config '"+executor.KubeadmConfig+"'" {
		t.Fatalf("third command = %q", runner.commands[2])
	}
	if !strings.Contains(runner.commands[3], "get --raw=/readyz") || !strings.Contains(runner.commands[8], "deployment/coredns") {
		t.Fatalf("Cilium health commands = %v", runner.commands[3:])
	}
	if !strings.Contains(runner.commands[9], "app.kubernetes.io/name=kubelift-registry") {
		t.Fatalf("Registry health command = %s", runner.commands[9])
	}
	if _, err := os.Stat(filepath.Join(executor.StagingRoot, "production", "bin", "kubeadm")); err != nil {
		t.Fatalf("stat staged kubeadm: %v", err)
	}
	contents, err := os.ReadFile(executor.KubeadmConfig)
	if err != nil {
		t.Fatalf("read kubeadm configuration: %v", err)
	}
	if !strings.Contains(string(contents), "imagePullPolicy: Never") {
		t.Fatalf("kubeadm configuration does not enforce offline images:\n%s", contents)
	}
	info, err := os.Stat(executor.KubeadmConfig)
	if err != nil {
		t.Fatalf("stat kubeadm configuration: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("kubeadm configuration permission = %o, want 600", permission)
	}
	ciliumContents, err := os.ReadFile(executor.CiliumManifest)
	if err != nil {
		t.Fatalf("read rendered Cilium manifest: %v", err)
	}
	if !strings.Contains(string(ciliumContents), "10.0.0.10") || !strings.Contains(string(ciliumContents), "6443") {
		t.Fatalf("rendered Cilium manifest has wrong API endpoint:\n%s", ciliumContents)
	}
	registryContents, err := os.ReadFile(executor.RegistryManifest)
	if err != nil {
		t.Fatalf("read rendered Registry manifest: %v", err)
	}
	if !strings.Contains(string(registryContents), "containerPort: 5000") || !strings.Contains(string(registryContents), executor.RegistryStorage) {
		t.Fatalf("rendered Registry manifest has wrong settings:\n%s", registryContents)
	}
}

func TestCreateExecutorRejectsAmbiguousKubeadmResume(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	executor := CreateExecutor{
		Runner:           &createRunner{},
		Transport:        distribute.LocalTransport{},
		StagingRoot:      filepath.Join(root, "staging"),
		KubeadmConfig:    filepath.Join(root, "kubernetes", "init.yaml"),
		CiliumManifest:   filepath.Join(root, "kubernetes", "cilium.yaml"),
		RegistryManifest: filepath.Join(root, "kubernetes", "manifests", "registry.yaml"),
		RegistryStorage:  filepath.Join(root, "registry"),
		AdminKubeconfig:  filepath.Join(root, "kubernetes", "admin.conf"),
		StateRoot:        filepath.Join(root, "state"),
		EffectiveUserID:  func() int { return 0 },
	}
	configHash, err := configurationHash(configuration)
	if err != nil {
		t.Fatalf("configurationHash() error = %v", err)
	}
	bundleHash, err := fileSHA256(configuration.Spec.Offline.Bundle)
	if err != nil {
		t.Fatalf("fileSHA256() error = %v", err)
	}
	state := newCreateState(configuration, configHash, bundleHash)
	if err := saveCreateState(createStatePath(executor.StateRoot, configuration.Metadata.Name), state, PhaseKubeadmStarting); err != nil {
		t.Fatalf("saveCreateState() error = %v", err)
	}

	_, err = executor.ExecuteCreate(context.Background(), configuration, CreateOptions{Resume: true})
	if err == nil || !strings.Contains(err.Error(), "may be incomplete") {
		t.Fatalf("ExecuteCreate() error = %v, want ambiguous kubeadm error", err)
	}
}

func TestCreateExecutorRejectsNonRootBeforeStaging(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	executor := CreateExecutor{
		Runner:           &createRunner{},
		Transport:        distribute.LocalTransport{},
		StagingRoot:      filepath.Join(root, "staging"),
		KubeadmConfig:    filepath.Join(root, "kubernetes", "init.yaml"),
		CiliumManifest:   filepath.Join(root, "kubernetes", "cilium.yaml"),
		RegistryManifest: filepath.Join(root, "kubernetes", "manifests", "registry.yaml"),
		RegistryStorage:  filepath.Join(root, "registry"),
		AdminKubeconfig:  filepath.Join(root, "kubernetes", "admin.conf"),
		StateRoot:        filepath.Join(root, "state"),
		EffectiveUserID:  func() int { return 1000 },
	}

	_, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "must run as root") {
		t.Fatalf("ExecuteCreate() error = %v, want root error", err)
	}
	if _, err := os.Stat(executor.StagingRoot); !os.IsNotExist(err) {
		t.Fatalf("staging path exists after root rejection: %v", err)
	}
}

func TestCreateExecutorSkipsRegistryWhenDisabled(t *testing.T) {
	configuration := createTestConfig(t)
	configuration.Spec.Registry.Enabled = false
	root := t.TempDir()
	adminKubeconfig := filepath.Join(root, "kubernetes", "admin.conf")
	runner := &createRunner{adminKubeconfig: adminKubeconfig}
	executor := CreateExecutor{
		Runner:          runner,
		Transport:       distribute.LocalTransport{},
		StagingRoot:     filepath.Join(root, "staging"),
		KubeadmConfig:   filepath.Join(root, "kubernetes", "init.yaml"),
		CiliumManifest:  filepath.Join(root, "kubernetes", "cilium.yaml"),
		AdminKubeconfig: adminKubeconfig,
		StateRoot:       filepath.Join(root, "state"),
		EffectiveUserID: func() int { return 0 },
	}

	result, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{})
	if err != nil {
		t.Fatalf("ExecuteCreate() error = %v", err)
	}
	if result.Images.RegistryCount != 0 || result.RegistryReady {
		t.Fatalf("Registry was enabled unexpectedly: %+v", result)
	}
	if len(runner.commands) != 9 {
		t.Fatalf("command count = %d, want 9 without Registry", len(runner.commands))
	}
	for _, command := range runner.commands {
		if strings.Contains(command, "registry.tar") || strings.Contains(command, "kubelift-registry") {
			t.Fatalf("Registry command ran while disabled: %s", command)
		}
	}
}

func TestCreateExecutorRejectsExistingClusterBeforeStaging(t *testing.T) {
	configuration := createTestConfig(t)
	root := t.TempDir()
	adminConfig := filepath.Join(root, "kubernetes", "admin.conf")
	if err := os.MkdirAll(filepath.Dir(adminConfig), 0o700); err != nil {
		t.Fatalf("create Kubernetes directory: %v", err)
	}
	if err := os.WriteFile(adminConfig, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing admin kubeconfig: %v", err)
	}
	executor := CreateExecutor{
		Runner:           &createRunner{},
		Transport:        distribute.LocalTransport{},
		StagingRoot:      filepath.Join(root, "staging"),
		KubeadmConfig:    filepath.Join(root, "kubernetes", "init.yaml"),
		CiliumManifest:   filepath.Join(root, "kubernetes", "cilium.yaml"),
		RegistryManifest: filepath.Join(root, "kubernetes", "manifests", "registry.yaml"),
		RegistryStorage:  filepath.Join(root, "registry"),
		AdminKubeconfig:  adminConfig,
		StateRoot:        filepath.Join(root, "state"),
		EffectiveUserID:  func() int { return 0 },
	}

	_, err := executor.ExecuteCreate(context.Background(), configuration, CreateOptions{})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("ExecuteCreate() error = %v, want existing cluster error", err)
	}
	if _, err := os.Stat(executor.StagingRoot); !os.IsNotExist(err) {
		t.Fatalf("staging path exists after existing cluster rejection: %v", err)
	}
}

func createTestConfig(t *testing.T) config.Config {
	t.Helper()
	configuration := config.Default()
	configuration.APIVersion = config.APIVersion
	configuration.Kind = config.Kind
	configuration.Metadata.Name = "production"
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.Network.PodCIDR = "10.244.0.0/16"
	configuration.Spec.Network.ServiceCIDR = "10.96.0.0/12"
	configuration.Spec.Offline.Bundle = createTestBundle(t)
	configuration.Spec.SSH.PrivateKey = "/root/.ssh/id_ed25519"
	return configuration
}

func createTestBundle(t *testing.T) string {
	t.Helper()
	type payload struct {
		path string
		kind string
		role string
	}
	payloads := []payload{
		{path: "bin/kubeadm", kind: "binary", role: "kubeadm"},
		{path: "bin/kubelet", kind: "binary", role: "kubelet"},
		{path: "bin/kubectl", kind: "binary", role: "kubectl"},
		{path: "bin/crictl", kind: "binary", role: "cri-tool"},
		{path: "cri/containerd.tar.gz", kind: "runtime", role: "containerd"},
		{path: "etc/containerd/config.toml", kind: "config", role: "containerd-config"},
		{path: "etc/systemd/containerd.service", kind: "config", role: "systemd-unit"},
		{path: "etc/systemd/kubelet.service", kind: "config", role: "systemd-unit"},
		{path: "images/kubernetes.tar", kind: "image", role: "kubernetes-image"},
		{path: "images/cilium.tar", kind: "image", role: "cilium-image"},
		{path: "images/registry.tar", kind: "image", role: "registry-image"},
		{path: "manifests/cilium.yaml.tmpl", kind: "manifest", role: "cilium-manifest"},
		{path: "manifests/registry.yaml.tmpl", kind: "manifest", role: "registry-manifest"},
	}
	source := t.TempDir()
	files := make([]bundle.File, 0, len(payloads))
	for _, item := range payloads {
		contents := []byte(item.path)
		if item.role == "cilium-manifest" {
			contents = []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cilium-config\ndata:\n  k8s-service-host: \"{{ .APIServerHost }}\"\n  k8s-service-port: \"{{ .APIServerPort }}\"\n")
		}
		if item.role == "registry-manifest" {
			contents = []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: kubelift-registry\n  namespace: kube-system\n  labels:\n    app.kubernetes.io/name: kubelift-registry\nspec:\n  hostNetwork: true\n  containers:\n    - name: registry\n      image: registry:2.8.0\n      imagePullPolicy: Never\n      ports:\n        - containerPort: {{ .RegistryPort }}\n      readinessProbe:\n        httpGet:\n          path: /v2/\n          port: {{ .RegistryPort }}\n  volumes:\n    - name: data\n      hostPath:\n        path: {{ .RegistryStoragePath }}\n        type: DirectoryOrCreate\n")
		}
		path := filepath.Join(source, filepath.FromSlash(item.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create payload directory: %v", err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		hash := sha256.Sum256(contents)
		files = append(files, bundle.File{
			Path: item.path, Kind: item.kind, Role: item.role,
			Size: int64(len(contents)), SHA256: fmt.Sprintf("%x", hash),
		})
	}
	manifest := bundle.Manifest{
		APIVersion: bundle.APIVersion,
		Kind:       bundle.Kind,
		Metadata:   bundle.Metadata{Name: "production"},
		Spec: bundle.ManifestSpec{
			KubernetesVersion: "v1.28.15",
			Architecture:      "amd64",
			UbuntuVersions:    []string{"22.04"},
			Components: map[string]string{
				"containerd": "v1.7.27",
				"cilium":     "v1.14.0",
				"registry":   "v2.8.0",
			},
			Files: files,
		},
	}
	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal bundle manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, bundle.ManifestPath), manifestData, 0o600); err != nil {
		t.Fatalf("write bundle manifest: %v", err)
	}
	output := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if err := bundle.Create(source, output); err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	return output
}
