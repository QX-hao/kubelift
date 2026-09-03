package localexec

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerCapturesOutputAndExitCode(t *testing.T) {
	result, err := (Runner{}).Run(context.Background(), "printf 'ready'")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "ready" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}

	result, err = (Runner{}).Run(context.Background(), "printf 'failed' >&2; exit 7")
	if err == nil || !strings.Contains(err.Error(), "code 7") {
		t.Fatalf("Run() error = %v, want exit code", err)
	}
	if result.Stderr != "failed" || result.ExitCode != 7 {
		t.Fatalf("failure result = %+v", result)
	}
}

func TestRunnerRejectsEmptyCommand(t *testing.T) {
	if _, err := (Runner{}).Run(context.Background(), ""); err == nil {
		t.Fatal("Run() with empty command unexpectedly passed")
	}
}
