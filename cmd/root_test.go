package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/config"
)

const helperProcessEnvironment = "KUBELIFT_TEST_HELPER_PROCESS"

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnvironment) != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	rootCmd.SetArgs(os.Args[separator+1:])
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestRootHelpExposesSupportedCommands(t *testing.T) {
	output, err := runCLI(t, "--help")
	if err != nil {
		t.Fatalf("kubelift --help error = %v\n%s", err, output)
	}
	for _, command := range []string{"add", "bundle", "check", "config", "create", "status", "version"} {
		if !strings.Contains(output, command) {
			t.Errorf("root help does not contain %q:\n%s", command, output)
		}
	}
}

func TestCheckSSHRejectsUnusableAddress(t *testing.T) {
	output, err := runCLI(t, "check", "ssh", "127.0.0.1")
	if err == nil || !strings.Contains(output, "usable IPv4 address") {
		t.Fatalf("check ssh error = %v, output = %q", err, output)
	}
}

func TestBundlePushRejectsUnusableAddress(t *testing.T) {
	output, err := runCLI(t, "bundle", "push", "127.0.0.1")
	if err == nil || !strings.Contains(output, "usable IPv4 address") {
		t.Fatalf("bundle push error = %v, output = %q", err, output)
	}
}

func TestBundlePrepareRejectsUnusableAddress(t *testing.T) {
	output, err := runCLI(t, "bundle", "prepare", "127.0.0.1")
	if err == nil || !strings.Contains(output, "usable IPv4 address") {
		t.Fatalf("bundle prepare error = %v, output = %q", err, output)
	}
}

func TestBundleImportImagesRejectsUnusableAddress(t *testing.T) {
	output, err := runCLI(t, "bundle", "import-images", "127.0.0.1")
	if err == nil || !strings.Contains(output, "usable IPv4 address") {
		t.Fatalf("bundle import-images error = %v, output = %q", err, output)
	}
}

func TestBundleHelpExposesNodePreparation(t *testing.T) {
	output, err := runCLI(t, "bundle", "--help")
	if err != nil {
		t.Fatalf("bundle help error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "prepare") {
		t.Fatalf("bundle help does not contain prepare:\n%s", output)
	}
	if !strings.Contains(output, "import-images") {
		t.Fatalf("bundle help does not contain import-images:\n%s", output)
	}
}

func TestBundleHelpDoesNotExposePackageInstaller(t *testing.T) {
	output, err := runCLI(t, "bundle", "--help")
	if err != nil {
		t.Fatalf("bundle help error = %v\n%s", err, output)
	}
	if strings.Contains(output, "install <IPv4>") {
		t.Fatalf("bundle help exposes removed package installer:\n%s", output)
	}
}

func TestConfigInitAndValidateCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cluster.yaml")
	output, err := runCLI(t, "config", "init", "-o", path)
	if err != nil {
		t.Fatalf("config init error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "Created cluster configuration template") {
		t.Fatalf("config init output = %q", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%q", path)) {
		t.Fatalf("config init output does not include path %q: %q", path, output)
	}
	output, err = runCLI(t, "config", "validate", "-f", path)
	if err != nil {
		t.Fatalf("config validate error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "is valid for cluster \"production\"") {
		t.Fatalf("config validate output = %q", output)
	}
	output, err = runCLI(t, "config", "init", "-o", path)
	if err == nil || !strings.Contains(output, "already exists") {
		t.Fatalf("second config init error = %v, output = %q", err, output)
	}
}

func TestConfigCommandsUseSystemDefaultPath(t *testing.T) {
	if got := configInitCmd.Flag("output").DefValue; got != defaultClusterConfigPath {
		t.Fatalf("config init default output = %q, want %q", got, defaultClusterConfigPath)
	}
	if got := configValidateCmd.Flag("config").DefValue; got != defaultClusterConfigPath {
		t.Fatalf("config validate default config = %q, want %q", got, defaultClusterConfigPath)
	}
}

func TestConfigKubeadmRendersOfflineInitConfiguration(t *testing.T) {
	path := writeCLIConfiguration(t, true)
	output, err := runCLI(t, "config", "kubeadm", "-f", path)
	if err != nil {
		t.Fatalf("config kubeadm error = %v\n%s", err, output)
	}
	for _, expected := range []string{
		"kind: InitConfiguration",
		"kind: ClusterConfiguration",
		"kind: KubeletConfiguration",
		"imagePullPolicy: Never",
		"controlPlaneEndpoint: 10.0.0.100:6443",
		"- addon/kube-proxy",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("config kubeadm output does not contain %q:\n%s", expected, output)
		}
	}
}

func TestCreateAndAddCommandsFailClosedWithoutDryRun(t *testing.T) {
	path := writeCLIConfiguration(t, true)

	output, err := runCLI(t, "create", "-f", path, "--dry-run")
	if err != nil {
		t.Fatalf("create --dry-run error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "Dry-run plan: create cluster") || !strings.Contains(output, "install-cilium") {
		t.Fatalf("create --dry-run output = %q", output)
	}
	output, err = runCLI(t, "create", "-f", path)
	if err == nil || !strings.Contains(output, "execution is not enabled yet") {
		t.Fatalf("create error = %v, output = %q", err, output)
	}

	output, err = runCLI(
		t,
		"add", "node", "10.0.0.21",
		"-f", path,
		"--user", "ubuntu",
		"--port", "2222",
		"--key", "/tmp/other-key",
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("add node --dry-run error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "SSH: ubuntu@10.0.0.21:2222 using /tmp/other-key") {
		t.Fatalf("add node --dry-run output = %q", output)
	}
	output, err = runCLI(t, "add", "node", "10.0.0.21", "-f", path)
	if err == nil || !strings.Contains(output, "execution is not enabled yet") {
		t.Fatalf("add node error = %v, output = %q", err, output)
	}
}

func TestAddMasterRequiresStableEndpoint(t *testing.T) {
	path := writeCLIConfiguration(t, false)
	output, err := runCLI(t, "add", "master", "10.0.0.11", "-f", path, "--dry-run")
	if err == nil || !strings.Contains(output, "endpoint is required") {
		t.Fatalf("add master error = %v, output = %q", err, output)
	}
}

func TestBundleManifestCreateAndInspectCommands(t *testing.T) {
	source := t.TempDir()
	payloadPath := filepath.Join(source, "images", "kubernetes.tar")
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o755); err != nil {
		t.Fatalf("create payload directory: %v", err)
	}
	if err := os.WriteFile(payloadPath, []byte("image data"), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	output, err := runCLI(t, "bundle", "manifest", source)
	if err == nil || !strings.Contains(output, "required flag") {
		t.Fatalf("bundle manifest without metadata error = %v, output = %q", err, output)
	}

	output, err = runCLI(
		t,
		"bundle", "manifest", source,
		"--name", "kubernetes-v1-28-15-amd64",
		"--kubernetes-version", "v1.28.15",
		"--architecture", "amd64",
		"--ubuntu-version", "22.04,24.04",
		"--containerd-version", "v1.7.0",
		"--cilium-version", "v1.14.0",
		"--registry-version", "v2.8.0",
		"--artifact-role", "images/kubernetes.tar=kubernetes-image",
	)
	if err != nil {
		t.Fatalf("bundle manifest error = %v\n%s", err, output)
	}

	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.zst")
	output, err = runCLI(t, "bundle", "create", source, "-o", bundlePath)
	if err != nil {
		t.Fatalf("bundle create error = %v\n%s", err, output)
	}
	output, err = runCLI(t, "bundle", "inspect", bundlePath, "--files")
	if err != nil {
		t.Fatalf("bundle inspect error = %v\n%s", err, output)
	}
	for _, want := range []string{"Checksums: verified", "Authenticity: not verified", "Payloads:", "images/kubernetes.tar", "kubernetes-image"} {
		if !strings.Contains(output, want) {
			t.Errorf("bundle inspect output does not contain %q:\n%s", want, output)
		}
	}
}

func TestStatusAndVersionRejectInvalidArguments(t *testing.T) {
	output, err := runCLI(t, "status", "--timeout", "0s")
	if err == nil || !strings.Contains(output, "timeout must be greater than zero") {
		t.Fatalf("status error = %v, output = %q", err, output)
	}
	output, err = runCLI(t, "version", "extra")
	if err == nil || !strings.Contains(output, "extra") {
		t.Fatalf("version error = %v, output = %q", err, output)
	}
}

func runCLI(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	commandArguments := []string{"-test.run=^TestCLIHelperProcess$", "--"}
	commandArguments = append(commandArguments, arguments...)
	command := exec.Command(os.Args[0], commandArguments...)
	command.Env = append(os.Environ(), helperProcessEnvironment+"=1")
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeCLIConfiguration(t *testing.T, withEndpoint bool) string {
	t.Helper()
	contents := config.Template
	if withEndpoint {
		contents = strings.Replace(contents, "    # endpoint: 10.0.0.100:6443", "    endpoint: 10.0.0.100:6443", 1)
	}
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	return path
}
