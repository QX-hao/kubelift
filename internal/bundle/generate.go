package bundle

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type ManifestOptions struct {
	Name              string
	KubernetesVersion string
	Architecture      string
	UbuntuVersions    []string
	ContainerdVersion string
	CiliumVersion     string
	RegistryVersion   string
	ArtifactRoles     map[string]string
}

var conventionalArtifactRoles = map[string]string{
	"bin/kubeadm":                    "kubeadm",
	"bin/kubelet":                    "kubelet",
	"bin/kubectl":                    "kubectl",
	"bin/crictl":                     "cri-tool",
	"bin/conntrack":                  "cri-tool",
	"bin/runc":                       "runc",
	"cri/containerd.tar.gz":          "containerd",
	"etc/containerd/config.toml":     "containerd-config",
	"etc/systemd/containerd.service": "systemd-unit",
	"etc/systemd/kubelet.service":    "systemd-unit",
	"images/kubernetes.tar":          "kubernetes-image",
	"images/cilium.tar":              "cilium-image",
	"images/registry.tar":            "registry-image",
	"manifests/cilium.yaml.tmpl":     "cilium-manifest",
	"manifests/registry.yaml.tmpl":   "registry-manifest",
	"scripts/init.sh":                "init-script",
}

func WriteManifest(sourceDirectory string, options ManifestOptions) (string, error) {
	info, err := os.Stat(sourceDirectory)
	if err != nil {
		return "", fmt.Errorf("stat bundle source %q: %w", sourceDirectory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("bundle source %q must be a directory", sourceDirectory)
	}

	manifestPath := filepath.Join(sourceDirectory, ManifestPath)
	if _, err := os.Lstat(manifestPath); err == nil {
		return "", fmt.Errorf("bundle manifest %q already exists", manifestPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat bundle manifest %q: %w", manifestPath, err)
	}

	manifest := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: options.Name},
		Spec: ManifestSpec{
			KubernetesVersion: options.KubernetesVersion,
			Architecture:      options.Architecture,
			UbuntuVersions:    append([]string(nil), options.UbuntuVersions...),
			Components: map[string]string{
				"containerd": options.ContainerdVersion,
				"cilium":     options.CiliumVersion,
				"registry":   options.RegistryVersion,
			},
		},
	}
	if err := manifest.validateMetadata(); err != nil {
		return "", fmt.Errorf("validate bundle manifest metadata: %w", err)
	}
	files, err := scanPayloadFiles(sourceDirectory)
	if err != nil {
		return "", err
	}
	applyConventionalArtifactRoles(files)
	if err := applyArtifactRoles(files, options.ArtifactRoles); err != nil {
		return "", err
	}
	manifest.Spec.Files = files
	if err := manifest.Validate(); err != nil {
		return "", fmt.Errorf("validate generated bundle manifest: %w", err)
	}
	contents, err := yaml.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode generated bundle manifest: %w", err)
	}

	// 清单不得覆盖已有文件，避免重新生成哈希时悄悄改变已经发布的包定义。
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("bundle manifest %q already exists", manifestPath)
		}
		return "", fmt.Errorf("create bundle manifest %q: %w", manifestPath, err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(manifestPath)
		return "", fmt.Errorf("write bundle manifest %q: %w", manifestPath, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(manifestPath)
		return "", fmt.Errorf("close bundle manifest %q: %w", manifestPath, err)
	}
	return manifestPath, nil
}

func applyConventionalArtifactRoles(files []File) {
	for index := range files {
		if role, exists := conventionalArtifactRoles[files[index].Path]; exists {
			files[index].Role = role
		}
	}
}

// ParseArtifactRoles 将 path=role 形式的命令行参数转换为角色映射。
func ParseArtifactRoles(values []string) (map[string]string, error) {
	roles := make(map[string]string, len(values))
	for _, value := range values {
		path, role, ok := strings.Cut(value, "=")
		path = strings.TrimSpace(path)
		role = strings.TrimSpace(role)
		if !ok || path == "" || role == "" || !fs.ValidPath(path) || strings.Contains(path, `\`) {
			return nil, fmt.Errorf("artifact role must use a safe path=role value, got %q", value)
		}
		if _, exists := roles[path]; exists {
			return nil, fmt.Errorf("artifact role for %q was declared more than once", path)
		}
		roles[path] = role
	}
	return roles, nil
}

func applyArtifactRoles(files []File, roles map[string]string) error {
	if len(roles) == 0 {
		return nil
	}
	declared := make(map[string]int, len(files))
	for index, file := range files {
		declared[file.Path] = index
	}
	for path, role := range roles {
		index, exists := declared[path]
		if !exists {
			return fmt.Errorf("artifact role references undeclared payload %q", path)
		}
		if inferred := files[index].Role; inferred != "" && inferred != role {
			return fmt.Errorf("artifact role %q for conventional path %q conflicts with inferred role %q", role, path, inferred)
		}
		files[index].Role = role
	}
	return nil
}

func scanPayloadFiles(sourceDirectory string) ([]File, error) {
	files := make([]File, 0)
	var totalSize int64
	err := filepath.WalkDir(sourceDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceDirectory, path)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if !isPayloadDirectory(relative) {
				return fmt.Errorf("source directory %q is not a supported payload directory", relative)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source payload %q must not be a symbolic link", relative)
		}
		kind, supported := payloadKind(relative)
		if !supported {
			return fmt.Errorf("source payload %q is outside a supported payload directory", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source payload %q must be a regular file", relative)
		}
		if info.Size() <= 0 {
			return fmt.Errorf("source payload %q must not be empty", relative)
		}
		if info.Size() > maxPayloadFileSize {
			return fmt.Errorf("source payload %q exceeds %d bytes", relative, maxPayloadFileSize)
		}
		if len(files) >= maxPayloadFiles {
			return fmt.Errorf("bundle source must not contain more than %d payload files", maxPayloadFiles)
		}
		if totalSize > maxTotalPayloadSize-info.Size() {
			return fmt.Errorf("bundle source payloads must not exceed %d bytes", maxTotalPayloadSize)
		}

		hash, size, err := hashPayload(path)
		if err != nil {
			return fmt.Errorf("hash source payload %q: %w", relative, err)
		}
		if size != info.Size() {
			return fmt.Errorf("source payload %q changed while generating manifest", relative)
		}
		files = append(files, File{Path: relative, Kind: kind, Size: size, SHA256: hash})
		totalSize += size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan bundle source %q: %w", sourceDirectory, err)
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func payloadKind(path string) (string, bool) {
	for kind, directory := range kindDirectory {
		if strings.HasPrefix(path, directory) {
			return kind, true
		}
	}
	return "", false
}

func isPayloadDirectory(path string) bool {
	for _, directory := range kindDirectory {
		root := strings.TrimSuffix(directory, "/")
		if path == root || strings.HasPrefix(path, directory) {
			return true
		}
	}
	return false
}

func hashPayload(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), size, nil
}
