package bundle

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion                = "kubelift.io/v1alpha1"
	Kind                      = "Bundle"
	ManifestPath              = "manifest.yaml"
	maxManifestSize           = 1 << 20
	maxPayloadFiles           = 4096
	maxPayloadFileSize  int64 = 16 << 30
	maxTotalPayloadSize int64 = 64 << 30
	maxArchiveEntries         = maxPayloadFiles * 2
)

var (
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	versionPattern  = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var supportedUbuntuVersions = map[string]struct{}{
	"22.04": {},
	"24.04": {},
	"26.04": {},
}

// 每种载荷类型都对应 Bundle 中固定的目录，避免将运行时归档误当成可执行文件安装。
var supportedFileKinds = map[string]struct{}{
	"binary":   {},
	"config":   {},
	"image":    {},
	"manifest": {},
	"runtime":  {},
	"script":   {},
}

var kindDirectory = map[string]string{
	"binary":   "bin/",
	"config":   "etc/",
	"image":    "images/",
	"manifest": "manifests/",
	"runtime":  "cri/",
	"script":   "scripts/",
}

var artifactRoleKinds = map[string]map[string]struct{}{
	"kubeadm":           {"binary": {}},
	"kubelet":           {"binary": {}},
	"kubectl":           {"binary": {}},
	"containerd":        {"runtime": {}},
	"runc":              {"binary": {}},
	"systemd-unit":      {"config": {}},
	"containerd-config": {"config": {}},
	"kubelet-config":    {"config": {}},
	"init-script":       {"script": {}},
	"cni-plugin":        {"binary": {}},
	"cri-tool":          {"binary": {}},
	"kubernetes-image":  {"image": {}},
	"cilium-image":      {"image": {}},
	"registry-image":    {"image": {}},
	"cilium-manifest":   {"manifest": {}},
	"registry-manifest": {"manifest": {}},
}

type Manifest struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   Metadata     `yaml:"metadata"`
	Spec       ManifestSpec `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type ManifestSpec struct {
	KubernetesVersion string            `yaml:"kubernetesVersion"`
	Architecture      string            `yaml:"architecture"`
	UbuntuVersions    []string          `yaml:"ubuntuVersions"`
	Components        map[string]string `yaml:"components"`
	Files             []File            `yaml:"files"`
}

type File struct {
	Path   string `yaml:"path"`
	Kind   string `yaml:"kind"`
	Role   string `yaml:"role,omitempty"`
	Size   int64  `yaml:"size"`
	SHA256 string `yaml:"sha256"`
}

// FilesForRole 返回清单中属于指定角色的载荷，供安装器按角色选择文件。
func (m Manifest) FilesForRole(role string) []File {
	files := make([]File, 0)
	for _, file := range m.Spec.Files {
		if file.Role == role {
			files = append(files, file)
		}
	}
	return files
}

func ParseManifest(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode bundle manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("decode bundle manifest: %w", err)
		}
		return nil, fmt.Errorf("bundle manifest must contain exactly one YAML document")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m Manifest) Validate() error {
	if err := m.validateMetadata(); err != nil {
		return err
	}
	if len(m.Spec.Files) == 0 {
		return fmt.Errorf("spec.files must contain at least one payload file")
	}
	if len(m.Spec.Files) > maxPayloadFiles {
		return fmt.Errorf("spec.files must not contain more than %d payload files", maxPayloadFiles)
	}

	seen := make(map[string]struct{}, len(m.Spec.Files))
	var totalSize int64
	for index, file := range m.Spec.Files {
		if err := validateFile(file); err != nil {
			return fmt.Errorf("spec.files[%d]: %w", index, err)
		}
		if _, exists := seen[file.Path]; exists {
			return fmt.Errorf("spec.files[%d]: duplicate path %q", index, file.Path)
		}
		seen[file.Path] = struct{}{}
		if totalSize > maxTotalPayloadSize-file.Size {
			return fmt.Errorf("spec.files total size must not exceed %d bytes", maxTotalPayloadSize)
		}
		totalSize += file.Size
	}
	return nil
}

func (m Manifest) validateMetadata() error {
	if m.APIVersion != APIVersion {
		return fmt.Errorf("bundle apiVersion must be %q", APIVersion)
	}
	if m.Kind != Kind {
		return fmt.Errorf("bundle kind must be %q", Kind)
	}
	if len(m.Metadata.Name) == 0 || len(m.Metadata.Name) > 63 || !dnsLabelPattern.MatchString(m.Metadata.Name) {
		return fmt.Errorf("bundle metadata.name must be a lowercase DNS label with at most 63 characters")
	}
	if !strings.HasPrefix(m.Spec.KubernetesVersion, "v") || !versionPattern.MatchString(m.Spec.KubernetesVersion) {
		return fmt.Errorf("spec.kubernetesVersion must be an exact version such as v1.28.15")
	}
	if m.Spec.Architecture != "amd64" && m.Spec.Architecture != "arm64" {
		return fmt.Errorf("spec.architecture must be amd64 or arm64")
	}
	if err := validateUbuntuVersions(m.Spec.UbuntuVersions); err != nil {
		return err
	}
	for _, name := range []string{"containerd", "cilium", "registry"} {
		version := m.Spec.Components[name]
		if !versionPattern.MatchString(version) {
			return fmt.Errorf("spec.components.%s must be an exact semantic version", name)
		}
	}
	return nil
}

func validateUbuntuVersions(versions []string) error {
	if len(versions) == 0 {
		return fmt.Errorf("spec.ubuntuVersions must contain at least one supported version")
	}
	seen := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		if _, supported := supportedUbuntuVersions[version]; !supported {
			return fmt.Errorf("spec.ubuntuVersions contains unsupported version %q", version)
		}
		if _, exists := seen[version]; exists {
			return fmt.Errorf("spec.ubuntuVersions contains duplicate version %q", version)
		}
		seen[version] = struct{}{}
	}
	return nil
}

func validateFile(file File) error {
	// fs.ValidPath 会拒绝绝对路径、空路径以及包含 . 或 .. 的归档路径。
	if !fs.ValidPath(file.Path) || strings.Contains(file.Path, `\`) || file.Path == ManifestPath {
		return fmt.Errorf("path %q is not a safe payload path", file.Path)
	}
	if _, supported := supportedFileKinds[file.Kind]; !supported {
		return fmt.Errorf("kind %q is not supported", file.Kind)
	}
	if !strings.HasPrefix(file.Path, kindDirectory[file.Kind]) {
		return fmt.Errorf("path %q does not match kind %q", file.Path, file.Kind)
	}
	if file.Role != "" {
		allowedKinds, supported := artifactRoleKinds[file.Role]
		if !supported {
			return fmt.Errorf("role %q is not supported", file.Role)
		}
		if _, allowed := allowedKinds[file.Kind]; !allowed {
			return fmt.Errorf("role %q cannot be used with kind %q", file.Role, file.Kind)
		}
	}
	if file.Size <= 0 {
		return fmt.Errorf("size must be greater than zero")
	}
	if file.Size > maxPayloadFileSize {
		return fmt.Errorf("size must not exceed %d bytes", maxPayloadFileSize)
	}
	if !sha256Pattern.MatchString(file.SHA256) {
		return fmt.Errorf("sha256 must contain 64 lowercase hexadecimal characters")
	}
	return nil
}
