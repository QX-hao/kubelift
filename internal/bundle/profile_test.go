package bundle

import (
	"strings"
	"testing"
)

func TestValidateClusterProfile(t *testing.T) {
	manifest := profileManifest()
	if err := ValidateClusterProfile(manifest, true); err != nil {
		t.Fatalf("ValidateClusterProfile() error = %v", err)
	}
}

func TestValidateClusterProfileReportsMissingRole(t *testing.T) {
	manifest := profileManifest()
	for index := range manifest.Spec.Files {
		if manifest.Spec.Files[index].Role == "cilium-image" {
			manifest.Spec.Files[index].Role = ""
		}
	}
	err := ValidateClusterProfile(manifest, true)
	if err == nil || !strings.Contains(err.Error(), "cilium-image") {
		t.Fatalf("ValidateClusterProfile() error = %v, want cilium-image error", err)
	}
}

func TestValidateClusterProfileAllowsDisabledRegistry(t *testing.T) {
	manifest := profileManifest()
	filtered := manifest.Spec.Files[:0]
	for _, file := range manifest.Spec.Files {
		if file.Role != "registry-image" && file.Role != "registry-manifest" {
			filtered = append(filtered, file)
		}
	}
	manifest.Spec.Files = filtered
	if err := ValidateClusterProfile(manifest, false); err != nil {
		t.Fatalf("ValidateClusterProfile() error = %v", err)
	}
}

func profileManifest() Manifest {
	roles := []struct{ path, kind, role string }{
		{"bin/kubeadm", "binary", "kubeadm"}, {"bin/kubelet", "binary", "kubelet"},
		{"bin/kubectl", "binary", "kubectl"}, {"cri/containerd.tar.gz", "runtime", "containerd"},
		{"etc/containerd/config.toml", "config", "containerd-config"},
		{"etc/systemd/containerd.service", "config", "systemd-unit"},
		{"etc/systemd/kubelet.service", "config", "systemd-unit"},
		{"images/kubernetes.tar", "image", "kubernetes-image"}, {"images/cilium.tar", "image", "cilium-image"},
		{"images/registry.tar", "image", "registry-image"},
		{"manifests/cilium.yaml.tmpl", "manifest", "cilium-manifest"},
		{"manifests/registry.yaml.tmpl", "manifest", "registry-manifest"},
	}
	files := make([]File, 0, len(roles))
	for _, item := range roles {
		files = append(files, File{Path: item.path, Kind: item.kind, Role: item.role, Size: 1, SHA256: strings.Repeat("0", 64)})
	}
	return Manifest{
		APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{Name: "test-bundle"},
		Spec: ManifestSpec{KubernetesVersion: "v1.28.15", Architecture: "amd64", UbuntuVersions: []string{"22.04"},
			Components: map[string]string{"containerd": "v1.7.0", "cilium": "v1.14.0", "registry": "v2.8.0"}, Files: files},
	}
}
