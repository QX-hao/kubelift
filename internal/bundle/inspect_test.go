package bundle

import (
	"archive/tar"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"gopkg.in/yaml.v3"
)

type archiveEntry struct {
	name     string
	contents []byte
	typeflag byte
}

func TestInspectVerifiesBundle(t *testing.T) {
	payload := []byte("container image data")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundle(t, manifest, []archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}})

	report, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if report.FileCount != 1 || report.TotalSize != int64(len(payload)) {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Manifest.Spec.Architecture != "amd64" {
		t.Fatalf("architecture = %q", report.Manifest.Spec.Architecture)
	}
}

func TestExtractVerifiesAndWritesPayloads(t *testing.T) {
	payload := []byte("container image data")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundle(t, manifest, []archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}})
	destination := filepath.Join(t.TempDir(), "payloads")

	report, err := Extract(path, destination)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if report.FileCount != 1 || report.TotalSize != int64(len(payload)) {
		t.Fatalf("unexpected report: %+v", report)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "images", "kubernetes.tar"))
	if err != nil {
		t.Fatalf("read extracted payload: %v", err)
	}
	if string(contents) != string(payload) {
		t.Fatalf("extracted payload = %q, want %q", contents, payload)
	}
	info, err := os.Stat(filepath.Join(destination, "images", "kubernetes.tar"))
	if err != nil {
		t.Fatalf("stat extracted payload: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("extracted payload permission = %o, want 600", permission)
	}
}

func TestExtractRequiresAbsoluteEmptyDestination(t *testing.T) {
	payload := []byte("image")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundle(t, manifest, []archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}})

	if _, err := Extract(path, "payloads"); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("Extract() relative destination error = %v", err)
	}
	destination := filepath.Join(t.TempDir(), "payloads")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "existing"), []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing destination file: %v", err)
	}
	if _, err := Extract(path, destination); err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("Extract() non-empty destination error = %v", err)
	}
}

func TestInspectRejectsTamperedPayload(t *testing.T) {
	manifest := validManifest("images/kubernetes.tar", []byte("expected"))
	path := writeBundle(t, manifest, []archiveEntry{{name: "images/kubernetes.tar", contents: []byte("tampered"), typeflag: tar.TypeReg}})

	_, err := Inspect(path)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 does not match") {
		t.Fatalf("Inspect() error = %v, want checksum error", err)
	}
}

func TestInspectRejectsUndeclaredPayload(t *testing.T) {
	payload := []byte("image")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundle(t, manifest, []archiveEntry{
		{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg},
		{name: "images/extra.tar", contents: []byte("extra"), typeflag: tar.TypeReg},
	})

	_, err := Inspect(path)
	if err == nil || !strings.Contains(err.Error(), "is not declared") {
		t.Fatalf("Inspect() error = %v, want undeclared payload error", err)
	}
}

func TestInspectRejectsUnsafeAndLinkedEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry archiveEntry
		want  string
	}{
		{name: "path traversal", entry: archiveEntry{name: "../outside", contents: []byte("bad"), typeflag: tar.TypeReg}, want: "unsafe path"},
		{name: "absolute path", entry: archiveEntry{name: "/outside", contents: []byte("bad"), typeflag: tar.TypeReg}, want: "unsafe path"},
		{name: "backslash path", entry: archiveEntry{name: `images\outside`, contents: []byte("bad"), typeflag: tar.TypeReg}, want: "unsafe path"},
		{name: "symbolic link", entry: archiveEntry{name: "images/link", typeflag: tar.TypeSymlink}, want: "unsupported entry type"},
		{name: "hard link", entry: archiveEntry{name: "images/link", typeflag: tar.TypeLink}, want: "unsupported entry type"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte("image")
			manifest := validManifest("images/kubernetes.tar", payload)
			path := writeBundle(t, manifest, []archiveEntry{
				{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg},
				test.entry,
			})

			_, err := Inspect(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inspect() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestInspectRequiresManifestAsFirstEntry(t *testing.T) {
	payload := []byte("image")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundleParts(
		t,
		manifest,
		[]archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}},
		[]archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}},
		nil,
	)

	_, err := Inspect(path)
	if err == nil || !strings.Contains(err.Error(), "manifest.yaml must be the first") {
		t.Fatalf("Inspect() error = %v, want first manifest error", err)
	}
}

func TestInspectRejectsTrailingData(t *testing.T) {
	payload := []byte("image")
	manifest := validManifest("images/kubernetes.tar", payload)
	path := writeBundleParts(
		t,
		manifest,
		nil,
		[]archiveEntry{{name: "images/kubernetes.tar", contents: payload, typeflag: tar.TypeReg}},
		[]byte("trailing data"),
	)

	_, err := Inspect(path)
	if err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("Inspect() error = %v, want trailing data error", err)
	}
}

func TestManifestRejectsExcessivePayloadBounds(t *testing.T) {
	t.Run("single file size", func(t *testing.T) {
		manifest := validManifest("images/kubernetes.tar", []byte("image"))
		manifest.Spec.Files[0].Size = maxPayloadFileSize + 1
		if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "must not exceed") {
			t.Fatalf("Validate() error = %v, want size limit error", err)
		}
	})

	t.Run("file count", func(t *testing.T) {
		manifest := validManifest("images/kubernetes.tar", []byte("image"))
		file := manifest.Spec.Files[0]
		manifest.Spec.Files = make([]File, maxPayloadFiles+1)
		for index := range manifest.Spec.Files {
			file.Path = fmt.Sprintf("images/file-%d.tar", index)
			manifest.Spec.Files[index] = file
		}
		if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "payload files") {
			t.Fatalf("Validate() error = %v, want file count error", err)
		}
	})
}

func TestManifestRejectsInvalidArtifactRole(t *testing.T) {
	manifest := validManifest("images/kubernetes.tar", []byte("image"))
	manifest.Spec.Files[0].Role = "not-a-role"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("Validate() error = %v, want role error", err)
	}

	manifest.Spec.Files[0].Role = "cilium-image"
	manifest.Spec.Files[0].Path = "cri/containerd.tar.gz"
	manifest.Spec.Files[0].Kind = "runtime"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be used") {
		t.Fatalf("Validate() error = %v, want role-kind error", err)
	}
}

func TestParseManifestRejectsUnknownField(t *testing.T) {
	data, err := yaml.Marshal(validManifest("images/kubernetes.tar", []byte("image")))
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	data = append(data, []byte("unknown: true\n")...)

	_, err = ParseManifest(data)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("ParseManifest() error = %v, want unknown field error", err)
	}
}

func validManifest(path string, payload []byte) Manifest {
	hash := sha256.Sum256(payload)
	return Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "kubernetes-v1-28-15-amd64"},
		Spec: ManifestSpec{
			KubernetesVersion: "v1.28.15",
			Architecture:      "amd64",
			UbuntuVersions:    []string{"22.04", "24.04", "26.04"},
			Components: map[string]string{
				"containerd": "v1.7.0",
				"cilium":     "v1.14.0",
				"registry":   "v2.8.0",
			},
			Files: []File{{
				Path:   path,
				Kind:   "image",
				Size:   int64(len(payload)),
				SHA256: fmt.Sprintf("%x", hash),
			}},
		},
	}
}

func writeBundle(t *testing.T, manifest Manifest, entries []archiveEntry) string {
	t.Helper()
	return writeBundleParts(t, manifest, nil, entries, nil)
}

func writeBundleParts(t *testing.T, manifest Manifest, prefix, entries []archiveEntry, trailing []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bundle.tar.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	encoder, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatalf("create zstd writer: %v", err)
	}
	archive := tar.NewWriter(encoder)
	for _, entry := range prefix {
		writeArchiveEntry(t, archive, entry)
	}

	manifestData, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeArchiveEntry(t, archive, archiveEntry{name: ManifestPath, contents: manifestData, typeflag: tar.TypeReg})
	for _, entry := range entries {
		writeArchiveEntry(t, archive, entry)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if len(trailing) > 0 {
		if _, err := encoder.Write(trailing); err != nil {
			t.Fatalf("write trailing data: %v", err)
		}
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close zstd writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close bundle: %v", err)
	}
	return path
}

func writeArchiveEntry(t *testing.T, archive *tar.Writer, entry archiveEntry) {
	t.Helper()

	header := &tar.Header{
		Name:     entry.name,
		Mode:     0o600,
		Size:     int64(len(entry.contents)),
		Typeflag: entry.typeflag,
	}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if entry.typeflag == tar.TypeReg {
		if _, err := archive.Write(entry.contents); err != nil {
			t.Fatalf("write tar payload: %v", err)
		}
	}
}
