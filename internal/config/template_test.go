package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteTemplateCreatesValidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cluster.yaml")

	if err := WriteTemplate(path); err != nil {
		t.Fatalf("WriteTemplate() error = %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("generated configuration is invalid: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated configuration: %v", err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("generated file permission = %o, want 600", permission)
	}
}

func TestWriteTemplateRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	const original = "user configuration\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write original configuration: %v", err)
	}

	err := WriteTemplate(path)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("WriteTemplate() error = %v, want already exists error", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original configuration: %v", err)
	}
	if string(contents) != original {
		t.Fatalf("existing configuration was modified: %q", contents)
	}
}

func TestTemplateMatchesRepositoryExample(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate current test file")
	}
	examplePath := filepath.Join(filepath.Dir(currentFile), "..", "..", "examples", "cluster.yaml")

	contents, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read repository example: %v", err)
	}
	if string(contents) != Template {
		t.Fatal("repository example does not match the built-in template")
	}
}
