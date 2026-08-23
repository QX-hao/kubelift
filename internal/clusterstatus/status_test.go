package clusterstatus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	name   string
	args   []string
	output []byte
	err    error
}

func (r *fakeRunner) combinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

func TestRunQueriesNodesWithConfiguredKubeconfig(t *testing.T) {
	kubeconfigPath := writeKubeconfig(t)
	runner := &fakeRunner{output: []byte("node output\n")}

	output, err := run(context.Background(), kubeconfigPath, runner)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if string(output) != "node output\n" {
		t.Fatalf("run() output = %q", output)
	}
	if runner.name != "kubectl" {
		t.Fatalf("command = %q, want kubectl", runner.name)
	}
	wantArgs := []string{"--kubeconfig", kubeconfigPath, "get", "nodes", "-o", "wide"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestRunRejectsMissingKubeconfig(t *testing.T) {
	runner := &fakeRunner{}

	_, err := run(context.Background(), filepath.Join(t.TempDir(), "missing.conf"), runner)
	if err == nil || !strings.Contains(err.Error(), "stat kubeconfig") {
		t.Fatalf("run() error = %v, want missing kubeconfig error", err)
	}
	if runner.name != "" {
		t.Fatal("kubectl should not run when kubeconfig is missing")
	}
}

func TestRunIncludesKubectlFailureOutput(t *testing.T) {
	kubeconfigPath := writeKubeconfig(t)
	runner := &fakeRunner{
		output: []byte("connection refused\n"),
		err:    errors.New("exit status 1"),
	}

	_, err := run(context.Background(), kubeconfigPath, runner)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("run() error = %v, want kubectl output", err)
	}
}

func writeKubeconfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "admin.conf")
	if err := os.WriteFile(path, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}
