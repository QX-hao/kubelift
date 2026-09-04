package kubeadm

import (
	"strings"
	"testing"
)

const testCAHash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseJoinCommandAndGenerateWorkerConfig(t *testing.T) {
	credentials, err := ParseJoinCommand("kubeadm join 10.0.0.100:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash " + testCAHash + "\n")
	if err != nil {
		t.Fatalf("ParseJoinCommand() error = %v", err)
	}
	contents, err := GenerateWorkerJoinConfig(credentials, "worker-1")
	if err != nil {
		t.Fatalf("GenerateWorkerJoinConfig() error = %v", err)
	}
	for _, expected := range []string{"kind: JoinConfiguration", "apiServerEndpoint: 10.0.0.100:6443", "name: worker-1", "imagePullPolicy: Never", testCAHash} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("join configuration does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestParseJoinCommandRejectsAdditionalShell(t *testing.T) {
	_, err := ParseJoinCommand("kubeadm join 10.0.0.100:6443 --token abcdef.0123456789abcdef --discovery-token-ca-cert-hash " + testCAHash + "; reboot")
	if err == nil {
		t.Fatal("ParseJoinCommand() error = nil, want rejection")
	}
}

func TestGenerateControlPlaneJoinConfig(t *testing.T) {
	credentials := JoinCredentials{APIServerEndpoint: "10.0.0.100:6443", Token: "abcdef.0123456789abcdef", CACertHash: testCAHash}
	certificateKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	contents, err := GenerateControlPlaneJoinConfig(credentials, "master-1", "10.0.0.11", certificateKey)
	if err != nil {
		t.Fatalf("GenerateControlPlaneJoinConfig() error = %v", err)
	}
	for _, expected := range []string{"controlPlane:", "advertiseAddress: 10.0.0.11", "bindPort: 6443", "certificateKey: " + certificateKey} {
		if !strings.Contains(string(contents), expected) {
			t.Errorf("control-plane join configuration does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestParseCertificateKeyRequiresExactlyOneKey(t *testing.T) {
	key := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	parsed, err := ParseCertificateKey("[upload-certs] Using certificate key:\n" + key + "\n")
	if err != nil || parsed != key {
		t.Fatalf("ParseCertificateKey() = %q, %v", parsed, err)
	}
	if _, err := ParseCertificateKey(key + "\n" + key); err == nil {
		t.Fatal("ParseCertificateKey() accepted multiple keys")
	}
}
