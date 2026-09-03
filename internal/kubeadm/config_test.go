package kubeadm

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/config"
	"gopkg.in/yaml.v3"
)

func TestGenerateInitConfigBuildsOfflineV128Configuration(t *testing.T) {
	configuration := validClusterConfig()
	configuration.Spec.ControlPlane.Endpoint = "api.k8s.example:6443"

	contents, err := GenerateInitConfig(configuration)
	if err != nil {
		t.Fatalf("GenerateInitConfig() error = %v", err)
	}
	for _, expected := range []string{
		"apiVersion: kubeadm.k8s.io/v1beta3",
		"kind: InitConfiguration",
		"criSocket: unix:///run/containerd/containerd.sock",
		"imagePullPolicy: Never",
		"advertiseAddress: 10.0.0.10",
		"- addon/kube-proxy",
		"kind: ClusterConfiguration",
		"clusterName: production",
		"kubernetesVersion: v1.28.15",
		"controlPlaneEndpoint: api.k8s.example:6443",
		"podSubnet: 10.244.0.0/16",
		"serviceSubnet: 10.96.0.0/12",
		"- api.k8s.example",
		"kind: KubeletConfiguration",
		"cgroupDriver: systemd",
	} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("generated configuration does not contain %q:\n%s", expected, contents)
		}
	}
	if documents := decodeDocuments(t, contents); len(documents) != 3 {
		t.Fatalf("document count = %d, want 3", len(documents))
	}
}

func TestGenerateInitConfigOmitsMissingControlPlaneEndpoint(t *testing.T) {
	contents, err := GenerateInitConfig(validClusterConfig())
	if err != nil {
		t.Fatalf("GenerateInitConfig() error = %v", err)
	}
	if strings.Contains(string(contents), "controlPlaneEndpoint:") {
		t.Fatalf("configuration contains empty controlPlaneEndpoint:\n%s", contents)
	}
}

func TestGenerateInitConfigRejectsUnsupportedKubernetesMinor(t *testing.T) {
	configuration := validClusterConfig()
	configuration.Spec.Kubernetes.Version = "v1.29.1"
	_, err := GenerateInitConfig(configuration)
	if err == nil || !strings.Contains(err.Error(), "v1.28.x only") {
		t.Fatalf("GenerateInitConfig() error = %v, want version compatibility error", err)
	}
}

func decodeDocuments(t *testing.T, contents []byte) []map[string]any {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	documents := make([]map[string]any, 0)
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode generated configuration: %v", err)
		}
		if len(document) != 0 {
			documents = append(documents, document)
		}
	}
	return documents
}

func validClusterConfig() config.Config {
	configuration := config.Default()
	configuration.APIVersion = config.APIVersion
	configuration.Kind = config.Kind
	configuration.Metadata.Name = "production"
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.Network.PodCIDR = "10.244.0.0/16"
	configuration.Spec.Network.ServiceCIDR = "10.96.0.0/12"
	configuration.Spec.Offline.Bundle = "/opt/kubelift/bundle.tar.zst"
	configuration.Spec.SSH.PrivateKey = "/root/.ssh/id_ed25519"
	return configuration
}
