package preflight

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/remote"
)

type fakeRemoteRunner struct {
	results map[string]remote.CommandResult
}

func (r fakeRemoteRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	result, ok := r.results[command]
	if !ok {
		return remote.CommandResult{Stderr: "command not configured"}, fmt.Errorf("command %q was not configured", command)
	}
	return result, nil
}

func TestCheckRemotePassesSupportedHost(t *testing.T) {
	runner := fakeRemoteRunner{results: map[string]remote.CommandResult{
		"hostname":                   {Stdout: "k8s2\n"},
		"uname -m":                   {Stdout: "x86_64\n"},
		"cat /etc/os-release":        {Stdout: "ID=ubuntu\nVERSION_ID=\"24.04\"\n"},
		"swapon --noheadings --show": {Stdout: ""},
	}}

	for _, result := range CheckRemote(context.Background(), runner) {
		if result.Err != nil {
			t.Errorf("check %q error = %v", result.Name, result.Err)
		}
		if result.Detail == "" {
			t.Errorf("check %q returned an empty detail", result.Name)
		}
	}
}

func TestCheckRemoteRejectsSwapAndUnsupportedOS(t *testing.T) {
	runner := fakeRemoteRunner{results: map[string]remote.CommandResult{
		"hostname":                   {Stdout: "k8s3\n"},
		"uname -m":                   {Stdout: "aarch64\n"},
		"cat /etc/os-release":        {Stdout: "ID=debian\nVERSION_ID=\"12\"\n"},
		"swapon --noheadings --show": {Stdout: "/swapfile file 2G 0B -2\n"},
	}}

	results := CheckRemote(context.Background(), runner)
	if len(results) != 4 {
		t.Fatalf("check count = %d, want 4", len(results))
	}
	if results[1].Err != nil {
		t.Fatalf("architecture check error = %v, want aarch64 to be supported", results[1].Err)
	}
	if results[2].Err == nil || !strings.Contains(results[2].Err.Error(), "requires Ubuntu") {
		t.Errorf("operating system error = %v, want Ubuntu error", results[2].Err)
	}
	if results[3].Err == nil || !strings.Contains(results[3].Err.Error(), "swap is enabled") {
		t.Errorf("swap error = %v, want enabled swap error", results[3].Err)
	}
}
