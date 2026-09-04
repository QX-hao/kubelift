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
	calls  [][]string
}

func (r *fakeRunner) combinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.output, r.err
}

func TestRunDetailsQueriesInstalledComponents(t *testing.T) {
	kubeconfigPath := writeKubeconfig(t)
	runner := &fakeRunner{output: []byte("NAME READY\n")}

	output, err := runDetails(context.Background(), kubeconfigPath, true, runner)
	if err != nil {
		t.Fatalf("runDetails() error = %v", err)
	}
	for _, heading := range []string{"[Nodes]", "[Cilium]", "[CoreDNS]", "[Registry]"} {
		if !strings.Contains(string(output), heading) {
			t.Errorf("detailed output does not contain %q:\n%s", heading, output)
		}
	}
	if len(runner.calls) != 4 {
		t.Fatalf("kubectl calls = %v, want 4", runner.calls)
	}
}

func TestRunDetailsSkipsDisabledRegistry(t *testing.T) {
	runner := &fakeRunner{output: []byte("ok\n")}
	output, err := runDetails(context.Background(), writeKubeconfig(t), false, runner)
	if err != nil {
		t.Fatalf("runDetails() error = %v", err)
	}
	if strings.Contains(string(output), "[Registry]") || len(runner.calls) != 3 {
		t.Fatalf("disabled Registry output = %q, calls = %v", output, runner.calls)
	}
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
