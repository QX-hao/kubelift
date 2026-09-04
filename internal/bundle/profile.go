package bundle

import (
	"fmt"
	"path/filepath"
)

// ValidateClusterProfile 确认 Bundle 具备创建和扩容 Kubernetes 集群所需的载荷角色。
func ValidateClusterProfile(manifest Manifest, registryEnabled bool) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	for _, role := range []string{"kubeadm", "kubelet", "kubectl", "containerd", "containerd-config", "cilium-manifest"} {
		if err := requireRoleCount(manifest, role, 1); err != nil {
			return err
		}
	}
	for _, role := range []string{"kubernetes-image", "cilium-image"} {
		if len(manifest.FilesForRole(role)) == 0 {
			return fmt.Errorf("cluster profile requires at least one %q payload", role)
		}
	}

	units := manifest.FilesForRole("systemd-unit")
	unitNames := make(map[string]int, len(units))
	for _, file := range units {
		unitNames[filepath.Base(filepath.FromSlash(file.Path))]++
	}
	for _, name := range []string{"containerd.service", "kubelet.service"} {
		if unitNames[name] != 1 {
			return fmt.Errorf("cluster profile requires exactly one %q systemd unit, found %d", name, unitNames[name])
		}
	}
	if registryEnabled {
		if len(manifest.FilesForRole("registry-image")) == 0 {
			return fmt.Errorf("cluster profile with Registry requires at least one %q payload", "registry-image")
		}
		if err := requireRoleCount(manifest, "registry-manifest", 1); err != nil {
			return err
		}
	}
	return nil
}

func requireRoleCount(manifest Manifest, role string, expected int) error {
	actual := len(manifest.FilesForRole(role))
	if actual != expected {
		return fmt.Errorf("cluster profile requires exactly %d %q payload, found %d", expected, role, actual)
	}
	return nil
}
