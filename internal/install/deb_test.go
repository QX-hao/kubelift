package install

import (
	"context"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

type fakeCommandRunner struct {
	command string
	result  remote.CommandResult
	err     error
}

func (r *fakeCommandRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.command = command
	return r.result, r.err
}

func TestInstallDebPackagesBuildsOfflineDpkgCommand(t *testing.T) {
	runner := &fakeCommandRunner{}
	count, err := InstallDebPackages(context.Background(), runner, "/var/lib/kubelift/staging/production", []bundle.File{
		{Path: "images/cilium.tar", Kind: "image"},
		{Path: "packages/z-dependency.deb", Kind: "package"},
		{Path: "packages/kubeadm.deb", Kind: "package"},
	})
	if err != nil {
		t.Fatalf("InstallDebPackages() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("package count = %d, want 2", count)
	}
	want := "dpkg --unpack -- '/var/lib/kubelift/staging/production/packages/kubeadm.deb' '/var/lib/kubelift/staging/production/packages/z-dependency.deb' && dpkg --configure --pending"
	if runner.command != want {
		t.Fatalf("command = %q, want %q", runner.command, want)
	}
}

func TestInstallDebPackagesRejectsMissingPackages(t *testing.T) {
	_, err := InstallDebPackages(context.Background(), &fakeCommandRunner{}, "/var/lib/kubelift/staging/production", []bundle.File{
		{Path: "images/kubernetes.tar", Kind: "image"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not contain package payloads") {
		t.Fatalf("error = %v, want missing package error", err)
	}
}

func TestInstallDebPackagesReportsRemoteFailure(t *testing.T) {
	runner := &fakeCommandRunner{
		result: remote.CommandResult{Stderr: "dependency is not configured"},
		err:    context.Canceled,
	}
	_, err := InstallDebPackages(context.Background(), runner, "/var/lib/kubelift/staging/production", []bundle.File{
		{Path: "packages/kubelet.deb", Kind: "package"},
	})
	if err == nil || !strings.Contains(err.Error(), "dependency is not configured") {
		t.Fatalf("error = %v, want remote stderr", err)
	}
}
