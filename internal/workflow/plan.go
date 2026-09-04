package workflow

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"

	"github.com/QX-hao/kubelift/internal/config"
)

type Role string

const (
	RoleMaster Role = "master"
	RoleNode   Role = "node"
)

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type Step struct {
	Name        string
	Description string
}

type Target struct {
	Address    string
	Name       string
	Role       Role
	SSHUser    string
	SSHPort    int
	PrivateKey string
}

type AddOptions struct {
	Address    string
	Name       string
	SSHUser    string
	SSHPort    int
	PrivateKey string
}

type Plan struct {
	Action            string
	Cluster           string
	KubernetesVersion string
	Target            *Target
	Steps             []Step
}

func CreatePlan(configuration config.Config) Plan {
	return Plan{
		Action:            "create cluster",
		Cluster:           configuration.Metadata.Name,
		KubernetesVersion: configuration.Spec.Kubernetes.Version,
		Steps: []Step{
			{Name: "preflight", Description: "Validate Master0, the offline bundle, and host requirements"},
			{Name: "prepare-host", Description: "Prepare the operating system for Kubernetes"},
			{Name: "install-runtime", Description: "Install and configure containerd"},
			{Name: "import-images", Description: "Import control-plane, Registry, and Cilium images into containerd"},
			{Name: "init-control-plane", Description: "Initialize Master0 with kubeadm without kube-proxy"},
			{Name: "install-cilium", Description: "Install Cilium with kube-proxy replacement enabled"},
			{Name: "verify-cluster", Description: "Wait for the control plane, Cilium, and CoreDNS to become healthy"},
			{Name: "start-registry", Description: "Start and verify the host-network Registry static Pod cache"},
		},
	}
}

func AddPlan(configuration config.Config, role Role, options AddOptions) (Plan, error) {
	if role != RoleMaster && role != RoleNode {
		return Plan{}, fmt.Errorf("unsupported node role %q", role)
	}
	address, err := validateTargetAddress(configuration, options.Address)
	if err != nil {
		return Plan{}, err
	}
	if role == RoleMaster && configuration.Spec.ControlPlane.Endpoint == "" {
		return Plan{}, fmt.Errorf("spec.controlPlane.endpoint is required before adding a master")
	}

	target := Target{
		Address:    address.String(),
		Name:       options.Name,
		Role:       role,
		SSHUser:    options.SSHUser,
		SSHPort:    options.SSHPort,
		PrivateKey: options.PrivateKey,
	}
	if target.SSHUser == "" {
		target.SSHUser = configuration.Spec.SSH.User
	}
	if target.SSHPort == 0 {
		target.SSHPort = configuration.Spec.SSH.Port
	}
	if target.PrivateKey == "" {
		target.PrivateKey = configuration.Spec.SSH.PrivateKey
	}
	if err := validateTarget(target); err != nil {
		return Plan{}, err
	}

	steps := []Step{
		{Name: "connect", Description: "Connect to the target over SSH and verify its host key"},
		{Name: "preflight", Description: "Validate the target Ubuntu host and Kubernetes prerequisites"},
		{Name: "prepare-host", Description: "Prepare the operating system for Kubernetes"},
		{Name: "install-components", Description: "Install containerd, kubelet, and kubeadm"},
		// 节点加入首版直接传输并导入镜像，不依赖集群内 Registry 可用性。
		{Name: "prepare-images", Description: "Transfer and import the required offline images directly"},
	}
	if role == RoleMaster {
		steps = append(steps,
			Step{Name: "join-control-plane", Description: "Upload certificates and join the control plane with kubeadm"},
			Step{Name: "verify-control-plane", Description: "Wait for the new API server and etcd member to become healthy"},
		)
	} else {
		steps = append(steps,
			Step{Name: "join-node", Description: "Join the worker node with kubeadm"},
			Step{Name: "verify-node", Description: "Wait for the node and Cilium agent to become ready"},
		)
	}

	return Plan{
		Action:            "add " + string(role),
		Cluster:           configuration.Metadata.Name,
		KubernetesVersion: configuration.Spec.Kubernetes.Version,
		Target:            &target,
		Steps:             steps,
	}, nil
}

func validateTargetAddress(configuration config.Config, value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	limitedBroadcast := netip.AddrFrom4([4]byte{255, 255, 255, 255})
	if err != nil ||
		!address.Is4() ||
		address.IsUnspecified() ||
		address.IsLoopback() ||
		address.IsMulticast() ||
		address.IsLinkLocalUnicast() ||
		address == limitedBroadcast {
		return netip.Addr{}, fmt.Errorf("target address must be a usable IPv4 address")
	}
	master0, err := netip.ParseAddr(configuration.Spec.ControlPlane.AdvertiseAddress)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse Master0 advertise address: %w", err)
	}
	if address == master0 {
		return netip.Addr{}, fmt.Errorf("target address must not be the Master0 advertise address")
	}
	if host, _, err := net.SplitHostPort(configuration.Spec.ControlPlane.Endpoint); err == nil {
		if endpointAddress, err := netip.ParseAddr(host); err == nil && address == endpointAddress {
			return netip.Addr{}, fmt.Errorf("target address must not be the control-plane endpoint")
		}
	}
	for name, value := range map[string]string{
		"Pod":     configuration.Spec.Network.PodCIDR,
		"Service": configuration.Spec.Network.ServiceCIDR,
	} {
		prefix, err := netip.ParsePrefix(value)
		if err == nil && prefix.Contains(address) {
			return netip.Addr{}, fmt.Errorf("target address must not belong to the %s CIDR", name)
		}
	}
	return address, nil
}

func validateTarget(target Target) error {
	if target.Name != "" && (len(target.Name) > 63 || !nodeNamePattern.MatchString(target.Name)) {
		return fmt.Errorf("node name must be a lowercase DNS label with at most 63 characters")
	}
	if target.SSHUser == "" {
		return fmt.Errorf("SSH user is required")
	}
	if target.SSHPort < 1 || target.SSHPort > 65535 {
		return fmt.Errorf("SSH port must be between 1 and 65535")
	}
	if !filepath.IsAbs(target.PrivateKey) {
		return fmt.Errorf("SSH private key must be an absolute path")
	}
	return nil
}
