package distribute

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalTransportUploadsAndVerifiesPayload(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "nested", "payload")
	contents := []byte("offline payload")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	transport := LocalTransport{}
	if err := transport.UploadFile(context.Background(), source, destination); err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	hash := sha256.Sum256(contents)
	if err := transport.VerifySHA256(context.Background(), destination, fmt.Sprintf("%x", hash)); err != nil {
		t.Fatalf("VerifySHA256() error = %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("destination permission = %o, want 600", permission)
	}
}

func TestLocalTransportCanceledCopyDoesNotReplaceDestination(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (LocalTransport{}).UploadFile(ctx, source, destination); err == nil {
		t.Fatal("UploadFile() with canceled context unexpectedly passed")
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(contents) != "existing" {
		t.Fatalf("destination was replaced after cancellation: %q", contents)
	}
}
