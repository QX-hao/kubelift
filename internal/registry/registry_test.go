package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/config"
	"github.com/QX-hao/kubelift/internal/remote"
)

type fakeRunner struct {
	command string
	result  remote.CommandResult
	err     error
}

func (r *fakeRunner) Run(_ context.Context, command string) (remote.CommandResult, error) {
	r.command = command
	return r.result, r.err
}

func TestRenderManifestBuildsIndependentStaticPod(t *testing.T) {
	contents, err := RenderManifest(testConfig(), "/var/lib/kubelift/registry", []byte(registryTemplate))
	if err != nil {
		t.Fatalf("RenderManifest() error = %v", err)
	}
	for _, expected := range []string{"hostNetwork: true", "containerPort: 5000", "path: /var/lib/kubelift/registry", "imagePullPolicy: Never"} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("rendered manifest does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestRenderManifestRejectsClusterDependentRegistry(t *testing.T) {
	invalid := strings.Replace(registryTemplate, "hostNetwork: true", "hostNetwork: false", 1)
	_, err := RenderManifest(testConfig(), "/var/lib/kubelift/registry", []byte(invalid))
	if err == nil || !strings.Contains(err.Error(), "hostNetwork") {
		t.Fatalf("RenderManifest() error = %v, want hostNetwork error", err)
	}

	invalid = strings.Replace(registryTemplate, "hostPath:", "persistentVolumeClaim:\n        claimName: registry", 1)
	_, err = RenderManifest(testConfig(), "/var/lib/kubelift/registry", []byte(invalid))
	if err == nil || !strings.Contains(err.Error(), "persistent volume claim") {
		t.Fatalf("RenderManifest() error = %v, want PVC error", err)
	}

	invalid = strings.Replace(registryTemplate, "path: /v2/", "path: /healthz", 1)
	_, err = RenderManifest(testConfig(), "/var/lib/kubelift/registry", []byte(invalid))
	if err == nil || !strings.Contains(err.Error(), "probe HTTP path /v2/") {
		t.Fatalf("RenderManifest() error = %v, want readiness probe error", err)
	}
}

func TestWaitReadyUsesMirrorPodLabel(t *testing.T) {
	runner := &fakeRunner{}
	if err := WaitReady(context.Background(), runner, "/etc/kubernetes/admin.conf"); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	for _, expected := range []string{"-n kube-system", "condition=Ready", "app.kubernetes.io/name=kubelift-registry", "--timeout=5m"} {
		if !strings.Contains(runner.command, expected) {
			t.Errorf("command does not contain %q: %s", expected, runner.command)
		}
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

const registryTemplate = `apiVersion: v1
kind: Pod
metadata:
  name: kubelift-registry
  namespace: kube-system
  labels:
    app.kubernetes.io/name: kubelift-registry
spec:
  hostNetwork: true
  containers:
    - name: registry
      image: registry:2.8.0
      imagePullPolicy: Never
      ports:
        - containerPort: {{ .RegistryPort }}
      readinessProbe:
        httpGet:
          path: /v2/
          port: {{ .RegistryPort }}
      volumeMounts:
        - name: data
          mountPath: /var/lib/registry
  volumes:
    - name: data
      hostPath:
        path: {{ .RegistryStoragePath }}
        type: DirectoryOrCreate
`
