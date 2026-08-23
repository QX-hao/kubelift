package preflight

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
	"gopkg.in/yaml.v3"
)

func TestLocalCheckerPassesSupportedHost(t *testing.T) {
	bundlePath := writeValidBundle(t)
	keyPath := writeFile(t, "id_ed25519", "private key", 0o600)
	osReleasePath := writeFile(t, "os-release", "ID=ubuntu\nVERSION_ID=\"24.04\"\n", 0o600)
	configuration := testConfiguration(bundlePath, keyPath)

	checker := localChecker{
		osReleasePath:   osReleasePath,
		operatingSystem: "linux",
		architecture:    "amd64",
		interfaceAddresses: func() ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.10")}, nil
		},
	}

	for _, result := range checker.check(configuration) {
		if result.Err != nil {
			t.Errorf("check %q error = %v", result.Name, result.Err)
		}
		if result.Detail == "" {
			t.Errorf("check %q returned an empty detail", result.Name)
		}
	}
}

func TestLocalCheckerReportsAllFailures(t *testing.T) {
	keyPath := writeFile(t, "id_ed25519", "private key", 0o644)
	osReleasePath := writeFile(t, "os-release", "ID=debian\nVERSION_ID=\"12\"\n", 0o600)
	configuration := testConfiguration(filepath.Join(t.TempDir(), "missing-bundle.tar.zst"), keyPath)

	checker := localChecker{
		osReleasePath:   osReleasePath,
		operatingSystem: "linux",
		architecture:    "386",
		interfaceAddresses: func() ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.11")}, nil
		},
	}

	results := checker.check(configuration)
	if len(results) != 5 {
		t.Fatalf("check count = %d, want 5", len(results))
	}
	for _, result := range results {
		if result.Err == nil {
			t.Errorf("check %q unexpectedly passed", result.Name)
		}
	}
}

func TestOfflineBundleRejectsHostAndConfigurationMismatch(t *testing.T) {
	tests := []struct {
		name               string
		kubernetesVersion  string
		bundleVersion      string
		hostArchitecture   string
		bundleArchitecture string
		hostUbuntu         string
		bundleUbuntu       []string
	}{
		{
			name:               "Kubernetes version",
			kubernetesVersion:  "v1.28.15",
			bundleVersion:      "v1.29.0",
			hostArchitecture:   "amd64",
			bundleArchitecture: "amd64",
			hostUbuntu:         "24.04",
			bundleUbuntu:       []string{"24.04"},
		},
		{
			name:               "architecture",
			kubernetesVersion:  "v1.28.15",
			bundleVersion:      "v1.28.15",
			hostArchitecture:   "arm64",
			bundleArchitecture: "amd64",
			hostUbuntu:         "24.04",
			bundleUbuntu:       []string{"24.04"},
		},
		{
			name:               "Ubuntu version",
			kubernetesVersion:  "v1.28.15",
			bundleVersion:      "v1.28.15",
			hostArchitecture:   "amd64",
			bundleArchitecture: "amd64",
			hostUbuntu:         "24.04",
			bundleUbuntu:       []string{"22.04"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundlePath := writeBundle(t, test.bundleVersion, test.bundleArchitecture, test.bundleUbuntu)
			osReleasePath := writeFile(t, "os-release", "ID=ubuntu\nVERSION_ID=\""+test.hostUbuntu+"\"\n", 0o600)
			configuration := testConfiguration(bundlePath, "/unused/key")
			configuration.Spec.Kubernetes.Version = test.kubernetesVersion
			checker := localChecker{
				osReleasePath:   osReleasePath,
				operatingSystem: "linux",
				architecture:    test.hostArchitecture,
			}

			if result := checker.checkOfflineBundle(configuration); result.Err == nil {
				t.Fatal("checkOfflineBundle() unexpectedly passed")
			}
		})
	}
}

func TestOperatingSystemSupportsConfiguredUbuntuVersions(t *testing.T) {
	for _, version := range []string{"22.04", "24.04", "26.04"} {
		t.Run(version, func(t *testing.T) {
			osReleasePath := writeFile(
				t,
				"os-release",
				"ID='ubuntu'\nVERSION_ID='"+version+"'\n",
				0o600,
			)
			checker := localChecker{
				osReleasePath:   osReleasePath,
				operatingSystem: "linux",
			}

			result := checker.checkOperatingSystem()
			if result.Err != nil {
				t.Fatalf("checkOperatingSystem() error = %v", result.Err)
			}
		})
	}
}

func TestCheckRegularFileRejectsEmptyAndInsecureFiles(t *testing.T) {
	tests := []struct {
		name       string
		contents   string
		permission os.FileMode
		private    bool
	}{
		{name: "empty", permission: 0o600},
		{name: "insecure private key", contents: "key", permission: 0o644, private: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeFile(t, "file", test.contents, test.permission)
			if result := checkRegularFile(test.name, path, test.private); result.Err == nil {
				t.Fatal("checkRegularFile() unexpectedly passed")
			}
		})
	}
}

func testConfiguration(bundlePath, keyPath string) config.Config {
	configuration := config.Default()
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.Offline.Bundle = bundlePath
	configuration.Spec.SSH.PrivateKey = keyPath
	return configuration
}

func writeValidBundle(t *testing.T) string {
	return writeBundle(t, "v1.28.15", "amd64", []string{"22.04", "24.04", "26.04"})
}

func writeBundle(t *testing.T, kubernetesVersion, architecture string, ubuntuVersions []string) string {
	t.Helper()

	source := t.TempDir()
	payload := []byte("container image data")
	payloadPath := filepath.Join(source, "images", "kubernetes.tar")
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o755); err != nil {
		t.Fatalf("create payload directory: %v", err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	hash := sha256.Sum256(payload)
	manifest := bundle.Manifest{
		APIVersion: bundle.APIVersion,
		Kind:       bundle.Kind,
		Metadata:   bundle.Metadata{Name: "kubernetes-v1-28-15-amd64"},
		Spec: bundle.ManifestSpec{
			KubernetesVersion: kubernetesVersion,
			Architecture:      architecture,
			UbuntuVersions:    ubuntuVersions,
			Components: map[string]string{
				"containerd": "v1.7.0",
				"cilium":     "v1.14.0",
				"registry":   "v2.8.0",
			},
			Files: []bundle.File{{
				Path:   "images/kubernetes.tar",
				Kind:   "image",
				Size:   int64(len(payload)),
				SHA256: fmt.Sprintf("%x", hash),
			}},
		},
	}
	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, bundle.ManifestPath), manifestData, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	output := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if err := bundle.Create(source, output); err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	return output
}

func writeFile(t *testing.T, name, contents string, permission os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), permission); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Chmod(path, permission); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	return path
}
