package bundle

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManifestScansAndHashesPayloads(t *testing.T) {
	source := t.TempDir()
	writePayload(t, source, "images/kubernetes.tar", "image")
	writePayload(t, source, "bin/kubectl", "binary")

	path, err := WriteManifest(source, validManifestOptions())
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	if path != filepath.Join(source, ManifestPath) {
		t.Fatalf("manifest path = %q", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	manifest, err := ParseManifest(contents)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if len(manifest.Spec.Files) != 2 {
		t.Fatalf("file count = %d, want 2", len(manifest.Spec.Files))
	}
	if manifest.Spec.Files[0].Path != "bin/kubectl" || manifest.Spec.Files[0].Kind != "binary" {
		t.Fatalf("first file = %+v", manifest.Spec.Files[0])
	}
	payloadHash := sha256.Sum256([]byte("binary"))
	if manifest.Spec.Files[0].SHA256 != fmt.Sprintf("%x", payloadHash) {
		t.Fatalf("SHA-256 = %q", manifest.Spec.Files[0].SHA256)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("manifest permission = %o, want 600", permission)
	}
}

func TestWriteManifestRefusesToOverwrite(t *testing.T) {
	source := t.TempDir()
	writePayload(t, source, "images/kubernetes.tar", "image")
	manifestPath := filepath.Join(source, ManifestPath)
	if err := os.WriteFile(manifestPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing manifest: %v", err)
	}

	_, err := WriteManifest(source, validManifestOptions())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("WriteManifest() error = %v, want already exists error", err)
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read existing manifest: %v", err)
	}
	if string(contents) != "existing" {
		t.Fatalf("existing manifest was modified: %q", contents)
	}
}

func TestWriteManifestRejectsUnsupportedAndUnsafePayloads(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		want    string
	}{
		{
			name: "unsupported directory",
			prepare: func(t *testing.T, source string) {
				writePayload(t, source, "other/file", "payload")
			},
			want: "not a supported payload directory",
		},
		{
			name: "empty payload",
			prepare: func(t *testing.T, source string) {
				writePayload(t, source, "images/empty.tar", "")
			},
			want: "must not be empty",
		},
		{
			name: "symbolic link",
			prepare: func(t *testing.T, source string) {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("payload"), 0o600); err != nil {
					t.Fatalf("write link target: %v", err)
				}
				if err := os.MkdirAll(filepath.Join(source, "images"), 0o755); err != nil {
					t.Fatalf("create images directory: %v", err)
				}
				if err := os.Symlink(target, filepath.Join(source, "images", "link.tar")); err != nil {
					t.Fatalf("create symbolic link: %v", err)
				}
			},
			want: "must not be a symbolic link",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := t.TempDir()
			test.prepare(t, source)
			_, err := WriteManifest(source, validManifestOptions())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("WriteManifest() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestWriteManifestRejectsInvalidMetadata(t *testing.T) {
	source := t.TempDir()
	writePayload(t, source, "images/kubernetes.tar", "image")
	options := validManifestOptions()
	options.Architecture = "386"

	_, err := WriteManifest(source, options)
	if err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("WriteManifest() error = %v, want architecture error", err)
	}
	if _, err := os.Stat(filepath.Join(source, ManifestPath)); !os.IsNotExist(err) {
		t.Fatalf("manifest should not exist after validation failure: %v", err)
	}
}

func validManifestOptions() ManifestOptions {
	return ManifestOptions{
		Name:              "kubernetes-v1-28-15-amd64",
		KubernetesVersion: "v1.28.15",
		Architecture:      "amd64",
		UbuntuVersions:    []string{"22.04", "24.04"},
		ContainerdVersion: "v1.7.0",
		CiliumVersion:     "v1.14.0",
		RegistryVersion:   "v2.8.0",
	}
}

func writePayload(t *testing.T, source, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(source, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create payload directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
}
