package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCreateBuildsInspectableBundle(t *testing.T) {
	source := writeBundleSource(t)
	output := filepath.Join(t.TempDir(), "bundle.tar.zst")

	if err := Create(source, output); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	report, err := Inspect(output)
	if err != nil {
		t.Fatalf("Inspect(created bundle) error = %v", err)
	}
	if report.FileCount != 1 {
		t.Fatalf("file count = %d, want 1", report.FileCount)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("output permission = %o, want 600", permission)
	}
}

func TestCreateRefusesToOverwriteOutput(t *testing.T) {
	source := writeBundleSource(t)
	output := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	err := Create(source, output)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Create() error = %v, want already exists error", err)
	}
	contents, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read existing output: %v", err)
	}
	if string(contents) != "existing" {
		t.Fatalf("existing output was modified: %q", contents)
	}
}

func TestCreateRejectsUndeclaredSourceFile(t *testing.T) {
	source := writeBundleSource(t)
	if err := os.WriteFile(filepath.Join(source, "extra"), []byte("extra"), 0o600); err != nil {
		t.Fatalf("write extra source file: %v", err)
	}

	err := Create(source, filepath.Join(t.TempDir(), "bundle.tar.zst"))
	if err == nil || !strings.Contains(err.Error(), "is not declared") {
		t.Fatalf("Create() error = %v, want undeclared source error", err)
	}
}

func TestCreateRejectsOutputInsideSourceDirectory(t *testing.T) {
	source := writeBundleSource(t)

	err := Create(source, filepath.Join(source, "bundle.tar.zst"))
	if err == nil || !strings.Contains(err.Error(), "outside the source directory") {
		t.Fatalf("Create() error = %v, want output location error", err)
	}
}

func TestCreateRejectsSymbolicLinkManifest(t *testing.T) {
	source := writeBundleSource(t)
	manifestPath := filepath.Join(source, ManifestPath)
	realManifestPath := filepath.Join(t.TempDir(), ManifestPath)
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(realManifestPath, contents, 0o600); err != nil {
		t.Fatalf("write real manifest: %v", err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if err := os.Symlink(realManifestPath, manifestPath); err != nil {
		t.Fatalf("link manifest: %v", err)
	}

	err = Create(source, filepath.Join(t.TempDir(), "bundle.tar.zst"))
	if err == nil || !strings.Contains(err.Error(), "must be a regular file") {
		t.Fatalf("Create() error = %v, want regular manifest error", err)
	}
}

func writeBundleSource(t *testing.T) string {
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
	manifestData, err := yaml.Marshal(validManifest("images/kubernetes.tar", payload))
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, ManifestPath), manifestData, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return source
}
