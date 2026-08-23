package workflow

import (
	"strings"
	"testing"

	"github.com/QX-hao/kubelift/internal/config"
)

func TestCreatePlanContainsBootstrapStages(t *testing.T) {
	plan := CreatePlan(testConfig())

	if plan.Action != "create cluster" || plan.Cluster != "production" {
		t.Fatalf("unexpected plan metadata: %+v", plan)
	}
	wantSteps := []string{
		"preflight",
		"prepare-host",
		"install-runtime",
		"import-images",
		"init-control-plane",
		"start-registry",
		"install-cilium",
		"verify-cluster",
	}
	if len(plan.Steps) != len(wantSteps) {
		t.Fatalf("step count = %d, want %d", len(plan.Steps), len(wantSteps))
	}
	for index, want := range wantSteps {
		if plan.Steps[index].Name != want {
			t.Errorf("step %d = %q, want %q", index, plan.Steps[index].Name, want)
		}
	}
}

func TestAddNodePlanUsesSSHDefaults(t *testing.T) {
	plan, err := AddPlan(testConfig(), RoleNode, AddOptions{Address: "10.0.0.21"})
	if err != nil {
		t.Fatalf("AddPlan() error = %v", err)
	}

	if plan.Target == nil {
		t.Fatal("AddPlan() target is nil")
	}
	if plan.Target.SSHUser != "root" || plan.Target.SSHPort != 22 {
		t.Fatalf("unexpected SSH defaults: %+v", plan.Target)
	}
	if plan.Target.PrivateKey != "/root/.ssh/id_ed25519" {
		t.Fatalf("private key = %q", plan.Target.PrivateKey)
	}
	if got := plan.Steps[len(plan.Steps)-1].Name; got != "verify-node" {
		t.Fatalf("last step = %q, want verify-node", got)
	}
}

func TestAddMasterPlanRequiresStableEndpoint(t *testing.T) {
	configuration := testConfig()
	configuration.Spec.ControlPlane.Endpoint = ""

	_, err := AddPlan(configuration, RoleMaster, AddOptions{Address: "10.0.0.11"})
	if err == nil || !strings.Contains(err.Error(), "endpoint is required") {
		t.Fatalf("AddPlan() error = %v, want endpoint required error", err)
	}
}

func TestAddPlanRejectsInvalidTargets(t *testing.T) {
	tests := []struct {
		name    string
		options AddOptions
		want    string
	}{
		{name: "invalid address", options: AddOptions{Address: "not-an-ip"}, want: "usable IPv4"},
		{name: "limited broadcast", options: AddOptions{Address: "255.255.255.255"}, want: "usable IPv4"},
		{name: "link-local address", options: AddOptions{Address: "169.254.1.10"}, want: "usable IPv4"},
		{name: "Master0 address", options: AddOptions{Address: "10.0.0.10"}, want: "Master0"},
		{name: "control-plane endpoint", options: AddOptions{Address: "10.0.0.100"}, want: "control-plane endpoint"},
		{name: "Pod address", options: AddOptions{Address: "10.244.0.10"}, want: "Pod CIDR"},
		{name: "invalid SSH port", options: AddOptions{Address: "10.0.0.21", SSHPort: 70000}, want: "SSH port"},
		{name: "relative private key", options: AddOptions{Address: "10.0.0.21", PrivateKey: "id_ed25519"}, want: "absolute path"},
		{name: "invalid node name", options: AddOptions{Address: "10.0.0.21", Name: "Worker_1"}, want: "DNS label"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AddPlan(testConfig(), RoleNode, test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AddPlan() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func testConfig() config.Config {
	configuration := config.Default()
	configuration.Metadata.Name = "production"
	configuration.Spec.Kubernetes.Version = "v1.28.15"
	configuration.Spec.ControlPlane.AdvertiseAddress = "10.0.0.10"
	configuration.Spec.ControlPlane.Endpoint = "10.0.0.100:6443"
	configuration.Spec.Network.PodCIDR = "10.244.0.0/16"
	configuration.Spec.Network.ServiceCIDR = "10.96.0.0/12"
	configuration.Spec.SSH.PrivateKey = "/root/.ssh/id_ed25519"
	return configuration
}
