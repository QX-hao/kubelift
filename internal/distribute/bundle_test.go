package distribute

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"gopkg.in/yaml.v3"
)

type fakeTransport struct {
	uploads   []string
	verified  []string
	checksums map[string]string
}

func (t *fakeTransport) UploadFile(_ context.Context, sourcePath, destinationPath string) error {
	contents, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	t.uploads = append(t.uploads, destinationPath+":"+string(contents))
	return nil
}

func (t *fakeTransport) VerifySHA256(_ context.Context, path, expected string) error {
	t.verified = append(t.verified, path)
	if t.checksums[path] != expected {
		return fmt.Errorf("unexpected checksum for %s", path)
	}
	return nil
}

func TestPushUploadsAndVerifiesAllBundlePayloads(t *testing.T) {
	bundlePath := writeTestBundle(t, map[string]string{
		"packages/kubeadm.deb": "kubeadm package",
		"images/cilium.tar":    "cilium image",
	})
	transport := &fakeTransport{checksums: map[string]string{
		"/var/lib/kubelift/staging/production/images/cilium.tar":    checksum("cilium image"),
		"/var/lib/kubelift/staging/production/packages/kubeadm.deb": checksum("kubeadm package"),
	}}

	report, err := Push(context.Background(), transport, bundlePath, "/var/lib/kubelift/staging/production")
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if report.RemoteRoot != "/var/lib/kubelift/staging/production" {
		t.Fatalf("remote root = %q", report.RemoteRoot)
	}
	sort.Strings(transport.uploads)
	wantUploads := []string{
		"/var/lib/kubelift/staging/production/images/cilium.tar:cilium image",
		"/var/lib/kubelift/staging/production/packages/kubeadm.deb:kubeadm package",
	}
	if fmt.Sprint(transport.uploads) != fmt.Sprint(wantUploads) {
		t.Fatalf("uploads = %v, want %v", transport.uploads, wantUploads)
	}
	sort.Strings(transport.verified)
	wantVerified := []string{
		"/var/lib/kubelift/staging/production/images/cilium.tar",
		"/var/lib/kubelift/staging/production/packages/kubeadm.deb",
	}
	if fmt.Sprint(transport.verified) != fmt.Sprint(wantVerified) {
		t.Fatalf("verified = %v, want %v", transport.verified, wantVerified)
	}
}

func TestPushRejectsInvalidArguments(t *testing.T) {
	if _, err := Push(context.Background(), nil, "/tmp/bundle.tar.zst", "/tmp/staging"); err == nil {
		t.Fatal("Push() with nil transport unexpectedly passed")
	}
	transport := &fakeTransport{}
	if _, err := Push(context.Background(), transport, "bundle.tar.zst", "/tmp/staging"); err == nil {
		t.Fatal("Push() with relative bundle path unexpectedly passed")
	}
	if _, err := Push(context.Background(), transport, "/tmp/bundle.tar.zst", "/"); err == nil {
		t.Fatal("Push() with root destination unexpectedly passed")
	}
}

func writeTestBundle(t *testing.T, payloads map[string]string) string {
	t.Helper()
	source := t.TempDir()
	files := make([]bundle.File, 0, len(payloads))
	for relativePath, contents := range payloads {
		path := filepath.Join(source, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create payload directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		hash := sha256.Sum256([]byte(contents))
		files = append(files, bundle.File{
			Path:   relativePath,
			Kind:   map[string]string{"packages": "package", "images": "image"}[filepath.ToSlash(filepath.Dir(relativePath))],
			Size:   int64(len(contents)),
			SHA256: fmt.Sprintf("%x", hash),
		})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	manifest := bundle.Manifest{
		APIVersion: bundle.APIVersion,
		Kind:       bundle.Kind,
		Metadata:   bundle.Metadata{Name: "kubernetes-v1-28-15-amd64"},
		Spec: bundle.ManifestSpec{
			KubernetesVersion: "v1.28.15",
			Architecture:      "amd64",
			UbuntuVersions:    []string{"22.04"},
			Components: map[string]string{
				"containerd": "v1.7.0",
				"cilium":     "v1.14.0",
				"registry":   "v2.8.0",
			},
			Files: files,
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

func checksum(value string) string {
	hash := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", hash)
}
