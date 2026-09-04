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

func TestPrepareNodeBuildsBinaryRuntimeAndSystemdCommand(t *testing.T) {
	runner := &fakeCommandRunner{}
	report, err := PrepareNode(context.Background(), runner, "/var/lib/kubelift/staging/production", preparationManifest())
	if err != nil {
		t.Fatalf("PrepareNode() error = %v", err)
	}
	if report.BinaryCount != 5 || report.RuntimeCount != 1 || report.ConfigCount != 1 || report.UnitCount != 2 {
		t.Fatalf("preparation report = %+v", report)
	}
	for _, expected := range []string{
		"/etc/modules-load.d/kubelift.conf",
		"modprobe overlay",
		"modprobe br_netfilter",
		"net.ipv4.ip_forward = 1",
		"sysctl --system",
		"install -m 0755 -- '/var/lib/kubelift/staging/production/bin/kubeadm' '/usr/bin/kubeadm'",
		"install -m 0755 -- '/var/lib/kubelift/staging/production/bin/crictl' '/usr/bin/crictl'",
		"install -m 0755 -- '/var/lib/kubelift/staging/production/bin/cilium-cni' '/opt/cni/bin/cilium-cni'",
		"tar -xzf '/var/lib/kubelift/staging/production/cri/containerd.tar.gz' --strip-components=1 -C /usr/bin",
		"install -m 0644 -- '/var/lib/kubelift/staging/production/etc/containerd/config.toml' /etc/containerd/config.toml",
		"install -m 0644 -- '/var/lib/kubelift/staging/production/etc/systemd/containerd.service' '/etc/systemd/system/containerd.service'",
		"systemctl daemon-reload && systemctl enable containerd.service && systemctl restart containerd.service && systemctl is-active --quiet containerd.service && systemctl enable kubelet.service && systemctl restart kubelet.service",
	} {
		if !strings.Contains(runner.command, expected) {
			t.Errorf("command does not contain %q:\n%s", expected, runner.command)
		}
	}
	if strings.Contains(runner.command, "dpkg") || strings.Contains(runner.command, "apt") {
		t.Fatalf("preparation command contains package installation: %s", runner.command)
	}
}

func TestPrepareNodeRequiresCorePayloads(t *testing.T) {
	manifest := preparationManifest()
	manifest.Spec.Files = manifest.Spec.Files[1:]
	_, err := PrepareNode(context.Background(), &fakeCommandRunner{}, "/var/lib/kubelift/staging/production", manifest)
	if err == nil || !strings.Contains(err.Error(), `"kubeadm" binary`) {
		t.Fatalf("PrepareNode() error = %v, want missing kubeadm error", err)
	}
}

func TestPrepareNodeReportsRemoteFailure(t *testing.T) {
	runner := &fakeCommandRunner{
		result: remote.CommandResult{Stderr: "Unit containerd.service failed"},
		err:    context.Canceled,
	}
	_, err := PrepareNode(context.Background(), runner, "/var/lib/kubelift/staging/production", preparationManifest())
	if err == nil || !strings.Contains(err.Error(), "Unit containerd.service failed") {
		t.Fatalf("PrepareNode() error = %v, want remote stderr", err)
	}
}

func TestPrepareNodeRejectsPackagePayloads(t *testing.T) {
	manifest := preparationManifest()
	manifest.Spec.Files[0].Kind = "package"
	_, err := PrepareNode(context.Background(), &fakeCommandRunner{}, "/var/lib/kubelift/staging/production", manifest)
	if err == nil || !strings.Contains(err.Error(), `kind "package" is not supported`) {
		t.Fatalf("PrepareNode() error = %v, want unsupported package kind", err)
	}
}

func preparationManifest() bundle.Manifest {
	files := []bundle.File{
		{Path: "bin/kubeadm", Kind: "binary", Role: "kubeadm"},
		{Path: "bin/kubelet", Kind: "binary", Role: "kubelet"},
		{Path: "bin/kubectl", Kind: "binary", Role: "kubectl"},
		{Path: "bin/crictl", Kind: "binary", Role: "cri-tool"},
		{Path: "bin/cilium-cni", Kind: "binary", Role: "cni-plugin"},
		{Path: "cri/containerd.tar.gz", Kind: "runtime", Role: "containerd"},
		{Path: "etc/containerd/config.toml", Kind: "config", Role: "containerd-config"},
		{Path: "etc/systemd/containerd.service", Kind: "config", Role: "systemd-unit"},
		{Path: "etc/systemd/kubelet.service", Kind: "config", Role: "systemd-unit"},
	}
	for index := range files {
		files[index].Size = 1
		files[index].SHA256 = strings.Repeat("0", 64)
	}
	return bundle.Manifest{
		APIVersion: bundle.APIVersion,
		Kind:       bundle.Kind,
		Metadata:   bundle.Metadata{Name: "production"},
		Spec: bundle.ManifestSpec{
			KubernetesVersion: "v1.28.15",
			Architecture:      "amd64",
			UbuntuVersions:    []string{"22.04"},
			Components: map[string]string{
				"containerd": "v1.7.27",
				"cilium":     "v1.14.0",
				"registry":   "v2.8.0",
			},
			Files: files,
		},
	}
}
