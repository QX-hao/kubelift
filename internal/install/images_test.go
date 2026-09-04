package install

import (
	"context"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/remote"
)

func TestImportImagesBuildsCtrCommandsByRole(t *testing.T) {
	runner := &fakeCommandRunner{}
	report, err := ImportImages(context.Background(), runner, "/var/lib/kubelift/staging/production", imageManifest(), ImageOptions{IncludeRegistry: true})
	if err != nil {
		t.Fatalf("ImportImages() error = %v", err)
	}
	if report.KubernetesCount != 1 || report.CiliumCount != 2 || report.RegistryCount != 1 {
		t.Fatalf("image report = %+v", report)
	}
	want := []string{
		"ctr -n k8s.io images import --all-platforms '/var/lib/kubelift/staging/production/images/kubernetes.tar'",
		"ctr -n k8s.io images import --all-platforms '/var/lib/kubelift/staging/production/images/cilium-agent.tar'",
		"ctr -n k8s.io images import --all-platforms '/var/lib/kubelift/staging/production/images/cilium-operator.tar'",
		"ctr -n k8s.io images import --all-platforms '/var/lib/kubelift/staging/production/images/registry.tar'",
	}
	for _, command := range want {
		if !strings.Contains(runner.command, command) {
			t.Errorf("command does not contain %q:\n%s", command, runner.command)
		}
	}
	if strings.Contains(runner.command, "docker") || strings.Contains(runner.command, "pull") {
		t.Fatalf("image import command unexpectedly accesses a registry: %s", runner.command)
	}
}

func TestImportImagesRequiresKubernetesAndCiliumImages(t *testing.T) {
	manifest := imageManifest()
	manifest.Spec.Files = manifest.Spec.Files[1:]
	_, err := ImportImages(context.Background(), &fakeCommandRunner{}, "/var/lib/kubelift/staging/production", manifest, ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), `"kubernetes-image" archive`) {
		t.Fatalf("ImportImages() error = %v, want missing Kubernetes image error", err)
	}

	manifest = imageManifest()
	manifest.Spec.Files = manifest.Spec.Files[:1]
	_, err = ImportImages(context.Background(), &fakeCommandRunner{}, "/var/lib/kubelift/staging/production", manifest, ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), `"cilium-image" archive`) {
		t.Fatalf("ImportImages() error = %v, want missing Cilium image error", err)
	}
}

func TestImportImagesReportsRemoteFailure(t *testing.T) {
	runner := &fakeCommandRunner{
		result: remote.CommandResult{Stderr: "ctr: failed to unpack image"},
		err:    context.Canceled,
	}
	_, err := ImportImages(context.Background(), runner, "/var/lib/kubelift/staging/production", imageManifest(), ImageOptions{})
	if err == nil || !strings.Contains(err.Error(), "ctr: failed to unpack image") {
		t.Fatalf("ImportImages() error = %v, want remote stderr", err)
	}
}

func TestImportImagesSkipsRegistryWhenDisabled(t *testing.T) {
	runner := &fakeCommandRunner{}
	report, err := ImportImages(context.Background(), runner, "/var/lib/kubelift/staging/production", imageManifest(), ImageOptions{})
	if err != nil {
		t.Fatalf("ImportImages() error = %v", err)
	}
	if report.RegistryCount != 0 || strings.Contains(runner.command, "registry.tar") {
		t.Fatalf("Registry image was imported while disabled: report=%+v command=%s", report, runner.command)
	}
}

func imageManifest() bundle.Manifest {
	manifest := preparationManifest()
	manifest.Spec.Files = []bundle.File{
		{Path: "images/kubernetes.tar", Kind: "image", Role: "kubernetes-image"},
		{Path: "images/cilium-agent.tar", Kind: "image", Role: "cilium-image"},
		{Path: "images/cilium-operator.tar", Kind: "image", Role: "cilium-image"},
		{Path: "images/registry.tar", Kind: "image", Role: "registry-image"},
	}
	for index := range manifest.Spec.Files {
		manifest.Spec.Files[index].Size = 1
		manifest.Spec.Files[index].SHA256 = strings.Repeat("0", 64)
	}
	return manifest
}
