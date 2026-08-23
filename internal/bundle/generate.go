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
