package cilium

import (
	"context"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/remote"
)

type fakeRunner struct {
	commands []string
	failAt   int
}

func (r *fakeRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.commands = append(r.commands, command)
	if r.failAt > 0 && len(r.commands) == r.failAt {
		return remote.CommandResult{Stderr: "not ready"}, context.Canceled
	}
	return remote.CommandResult{}, nil
}

func TestRenderManifestUsesStableControlPlaneEndpoint(t *testing.T) {
	configuration := testConfig()
	configuration.Spec.ControlPlane.Endpoint = "api.k8s.example:7443"
	source := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: cilium-config
data:
  k8s-service-host: "{{ .APIServerHost }}"
  k8s-service-port: "{{ .APIServerPort }}"
  cluster-name: "{{ .ClusterName }}"
  pod-cidr: "{{ .PodCIDR }}"
`)

	contents, err := RenderManifest(configuration, source)
	if err != nil {
		t.Fatalf("RenderManifest() error = %v", err)
	}
	for _, expected := range []string{"api.k8s.example", "7443", "production", "10.244.0.0/16"} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("rendered manifest does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestRenderManifestRequiresKubeProxyReplacementEndpointPlaceholders(t *testing.T) {
	_, err := RenderManifest(testConfig(), []byte("apiVersion: v1\nkind: ConfigMap\n"))
	if err == nil || !strings.Contains(err.Error(), "APIServerHost") {
		t.Fatalf("RenderManifest() error = %v, want placeholder error", err)
	}
}

func TestInstallAndWaitChecksCiliumNodesAndCoreDNS(t *testing.T) {
	runner := &fakeRunner{}
	err := InstallAndWait(context.Background(), runner, "/etc/kubernetes/cilium.yaml", "/etc/kubernetes/admin.conf")
	if err != nil {
		t.Fatalf("InstallAndWait() error = %v", err)
	}
	if len(runner.commands) != 6 {
		t.Fatalf("command count = %d, want 6", len(runner.commands))
	}
	for index, expected := range []string{
		"get --raw=/readyz",
		"apply -f '/etc/kubernetes/cilium.yaml'",
		"rollout status daemonset/cilium",
		"rollout status deployment/cilium-operator",
		"wait --for=condition=Ready nodes --all",
		"rollout status deployment/coredns",
	} {
		if !strings.Contains(runner.commands[index], expected) {
			t.Errorf("command %d = %q, want %q", index, runner.commands[index], expected)
		}
	}
}

func TestInstallAndWaitReportsFailedStage(t *testing.T) {
	runner := &fakeRunner{failAt: 3}
	err := InstallAndWait(context.Background(), runner, "/etc/kubernetes/cilium.yaml", "/etc/kubernetes/admin.conf")
	if err == nil || !strings.Contains(err.Error(), "wait for Cilium agents") || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("InstallAndWait() error = %v, want stage and stderr", err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("command count after failure = %d, want 3", len(runner.commands))
	}
}

func testConfig() config.Config {
	configuration := config.Default()
	configuration.APIVersion = config.APIVersion
	configuration.Kind = config.Kind
	configuration.Metadata.Name = "production"
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.Network.PodCIDR = "10.244.0.0/16"
	configuration.Spec.Network.ServiceCIDR = "10.96.0.0/12"
	configuration.Spec.Offline.Bundle = "/opt/kubelift/bundle.tar.zst"
	configuration.Spec.SSH.PrivateKey = "/root/.ssh/id_ed25519"
	return configuration
}
