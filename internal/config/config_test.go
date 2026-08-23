package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `apiVersion: kubelift.io/v1alpha1
kind: Cluster
metadata:
  name: production
spec:
  kubernetes:
    version: v1.28.15
  controlPlane:
    advertiseAddress: 10.0.0.10
  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
  offline:
    bundle: /opt/kubelift/bundle.tar.zst
  registry: {}
  ssh:
    privateKey: /root/.ssh/id_ed25519
`)

	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !configuration.Spec.Registry.Enabled {
		t.Fatal("registry should be enabled by default")
	}
	if configuration.Spec.Registry.Port != 5000 {
		t.Fatalf("registry port = %d, want 5000", configuration.Spec.Registry.Port)
	}
	if configuration.Spec.SSH.User != "root" {
		t.Fatalf("SSH user = %q, want root", configuration.Spec.SSH.User)
	}
	if configuration.Spec.SSH.Port != 22 {
		t.Fatalf("SSH port = %d, want 22", configuration.Spec.SSH.Port)
	}
}

func TestLoadAppliesDefaultsWhenRegistryIsOmitted(t *testing.T) {
	registryBlock := `  registry:
    enabled: true
    port: 5000
`
	path := writeConfig(t, strings.Replace(validYAML, registryBlock, "", 1))

	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !configuration.Spec.Registry.Enabled || configuration.Spec.Registry.Port != 5000 {
		t.Fatalf("registry = %+v, want enabled on port 5000", configuration.Spec.Registry)
	}
}

func TestLoadPreservesExplicitlyDisabledRegistry(t *testing.T) {
	path := writeConfig(t, strings.Replace(validYAML, "enabled: true", "enabled: false", 1))

	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if configuration.Spec.Registry.Enabled {
		t.Fatal("registry should remain disabled when explicitly configured")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	path := writeConfig(t, strings.Replace(
		validYAML,
		"    version: v1.28.15",
		"    version: v1.28.15\n    channel: stable",
		1,
	))

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field channel not found") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestLoadRejectsMultipleDocuments(t *testing.T) {
	path := writeConfig(t, validYAML+"---\n{}\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("Load() error = %v, want multiple document error", err)
	}
}

func TestValidateRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{
			name: "non-exact Kubernetes version",
			change: func(configuration *Config) {
				configuration.Spec.Kubernetes.Version = "v1.28"
			},
			want: "exact version",
		},
		{
			name: "loopback advertise address",
			change: func(configuration *Config) {
				configuration.Spec.ControlPlane.AdvertiseAddress = "127.0.0.1"
			},
			want: "usable unicast",
		},
		{
			name: "overlapping network ranges",
			change: func(configuration *Config) {
				configuration.Spec.Network.ServiceCIDR = "10.244.1.0/24"
			},
			want: "must not overlap",
		},
		{
			name: "non-canonical Pod range",
			change: func(configuration *Config) {
				configuration.Spec.Network.PodCIDR = "10.244.1.1/16"
			},
			want: "canonical network address",
		},
		{
			name: "advertise address in Pod range",
			change: func(configuration *Config) {
				configuration.Spec.ControlPlane.AdvertiseAddress = "10.244.0.10"
			},
			want: "must not belong",
		},
		{
			name: "loopback control-plane endpoint",
			change: func(configuration *Config) {
				configuration.Spec.ControlPlane.Endpoint = "127.0.0.1:6443"
			},
			want: "usable unicast",
		},
		{
			name: "relative bundle path",
			change: func(configuration *Config) {
				configuration.Spec.Offline.Bundle = "bundle.tar.zst"
			},
			want: "bundle must be an absolute path",
		},
		{
			name: "relative private key path",
			change: func(configuration *Config) {
				configuration.Spec.SSH.PrivateKey = "id_ed25519"
			},
			want: "privateKey must be an absolute path",
		},
		{
			name: "conflicting registry and SSH ports",
			change: func(configuration *Config) {
				configuration.Spec.Registry.Port = 22
			},
			want: "must not conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := validConfig()
			test.change(&configuration)

			err := configuration.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsReservedRegistryPorts(t *testing.T) {
	for port := range reservedRegistryPorts {
		configuration := validConfig()
		configuration.Spec.Registry.Port = port

		err := configuration.Validate()
		if err == nil || !strings.Contains(err.Error(), "control-plane port") {
			t.Errorf("Validate() with registry port %d error = %v, want control-plane port error", port, err)
		}
	}
}

func validConfig() Config {
	configuration := Default()
	configuration.APIVersion = APIVersion
	configuration.Kind = Kind
	configuration.Metadata.Name = "production"
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.Network.PodCIDR = "10.244.0.0/16"
	configuration.Spec.Network.ServiceCIDR = "10.96.0.0/12"
	configuration.Spec.Offline.Bundle = "/opt/kubelift/bundle.tar.zst"
	configuration.Spec.SSH.PrivateKey = "/root/.ssh/id_ed25519"
	return configuration
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const validYAML = `apiVersion: kubelift.io/v1alpha1
kind: Cluster
metadata:
  name: production
spec:
  kubernetes:
    version: v1.28.15
  controlPlane:
    advertiseAddress: 10.0.0.10
  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
  offline:
    bundle: /opt/kubelift/bundle.tar.zst
  registry:
    enabled: true
    port: 5000
  ssh:
    user: root
    port: 22
    privateKey: /root/.ssh/id_ed25519
`
