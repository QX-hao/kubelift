package preflight

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

type fakeRemoteRunner struct {
	results map[string]remote.CommandResult
}

func TestCheckRemoteBundleRejectsPlatformMismatch(t *testing.T) {
	runner := fakeRemoteRunner{results: map[string]remote.CommandResult{
		"uname -m":            {Stdout: "aarch64\n"},
		"cat /etc/os-release": {Stdout: "ID=ubuntu\nVERSION_ID=24.04\n"},
	}}
	manifest := bundle.Manifest{Spec: bundle.ManifestSpec{Architecture: "amd64", UbuntuVersions: []string{"22.04"}}}
	results := CheckRemoteBundle(context.Background(), runner, manifest)
	if len(results) != 2 || results[0].Err == nil || results[1].Err == nil {
		t.Fatalf("CheckRemoteBundle() = %+v, want architecture and Ubuntu errors", results)
	}
	if !strings.Contains(results[0].Err.Error(), "requires x86_64") || !strings.Contains(results[1].Err.Error(), "supports Ubuntu 22.04") {
		t.Fatalf("compatibility errors = %v, %v", results[0].Err, results[1].Err)
	}
}

func TestCheckRemotePortsRejectsOccupiedKubernetesPort(t *testing.T) {
	runner := fakeRemoteRunner{results: map[string]remote.CommandResult{
		"ss -H -lnt": {Stdout: "LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*\nLISTEN 0 4096 [::]:10250 [::]:*\n"},
	}}
	result := CheckRemotePorts(context.Background(), runner, []int{6443, 10250})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "10250") {
		t.Fatalf("CheckRemotePorts() = %+v, want occupied 10250", result)
	}
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
		"getconf _NPROCESSORS_ONLN":  {Stdout: "2\n"},
		"awk '/MemTotal:/ {print $2}' /proc/meminfo":    {Stdout: "2097152\n"},
		"df -Pk /var/lib | awk 'NR==2 {print $4}'":      {Stdout: "52428800\n"},
		"test -d /run/systemd/system && printf systemd": {Stdout: "systemd"},
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
		"getconf _NPROCESSORS_ONLN":  {Stdout: "1\n"},
		"awk '/MemTotal:/ {print $2}' /proc/meminfo":    {Stdout: "1048576\n"},
		"df -Pk /var/lib | awk 'NR==2 {print $4}'":      {Stdout: "1024\n"},
		"test -d /run/systemd/system && printf systemd": {Stdout: ""},
	}}

	results := CheckRemote(context.Background(), runner)
	if len(results) != 8 {
		t.Fatalf("check count = %d, want 8", len(results))
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
