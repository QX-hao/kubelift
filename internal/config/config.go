package config

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	APIVersion = "kubelift.io/v1alpha1"
	Kind       = "Cluster"
)

var (
	clusterNamePattern       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubernetesVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*\.[0-9]+\.[0-9]+$`)
	hostnameLabelPattern     = regexp.MustCompile(`^[a-zA-Z0-9]([-a-zA-Z0-9]*[a-zA-Z0-9])?$`)
	// Registry 使用 hostNetwork，不能占用 kubeadm 控制面组件的固定端口。
	reservedRegistryPorts = map[int]struct{}{
		2379:  {},
		2380:  {},
		6443:  {},
		10250: {},
		10257: {},
		10259: {},
	}
)

type Config struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Spec struct {
	Kubernetes   KubernetesSpec   `yaml:"kubernetes"`
	ControlPlane ControlPlaneSpec `yaml:"controlPlane"`
	Network      NetworkSpec      `yaml:"network"`
	Offline      OfflineSpec      `yaml:"offline"`
	Registry     RegistrySpec     `yaml:"registry"`
	SSH          SSHSpec          `yaml:"ssh"`
}

type KubernetesSpec struct {
	Version string `yaml:"version"`
}

type ControlPlaneSpec struct {
	AdvertiseAddress string `yaml:"advertiseAddress"`
	Endpoint         string `yaml:"endpoint,omitempty"`
}

type NetworkSpec struct {
	PodCIDR     string `yaml:"podCIDR"`
	ServiceCIDR string `yaml:"serviceCIDR"`
}

type OfflineSpec struct {
	Bundle string `yaml:"bundle"`
}

type RegistrySpec struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type SSHSpec struct {
	User       string `yaml:"user"`
	Port       int    `yaml:"port"`
	PrivateKey string `yaml:"privateKey"`
}

func Default() Config {
	// 先写入默认值，YAML 中显式配置的值仍会在解码时覆盖它们。
	return Config{
		Spec: Spec{
			Registry: RegistrySpec{
				Enabled: true,
				Port:    5000,
			},
			SSH: SSHSpec{
				User: "root",
				Port: 22,
			},
		},
	}
}

func (c Config) Validate() error {
	if c.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if c.Kind != Kind {
		return fmt.Errorf("kind must be %q", Kind)
	}
	if err := validateClusterName(c.Metadata.Name); err != nil {
		return fmt.Errorf("metadata.name: %w", err)
	}
	if !kubernetesVersionPattern.MatchString(c.Spec.Kubernetes.Version) {
		return fmt.Errorf("spec.kubernetes.version must be an exact version such as v1.28.15")
	}
	if err := validateAdvertiseAddress(c.Spec.ControlPlane.AdvertiseAddress); err != nil {
		return fmt.Errorf("spec.controlPlane.advertiseAddress: %w", err)
	}
	if c.Spec.ControlPlane.Endpoint != "" {
		if err := validateEndpoint(c.Spec.ControlPlane.Endpoint); err != nil {
			return fmt.Errorf("spec.controlPlane.endpoint: %w", err)
		}
	}
	if err := validateNetwork(c.Spec.Network, c.Spec.ControlPlane.AdvertiseAddress); err != nil {
		return err
	}
	if !filepath.IsAbs(c.Spec.Offline.Bundle) {
		return fmt.Errorf("spec.offline.bundle must be an absolute path")
	}
	if err := validatePort(c.Spec.Registry.Port); err != nil {
		return fmt.Errorf("spec.registry.port: %w", err)
	}
	if c.Spec.Registry.Enabled {
		if _, reserved := reservedRegistryPorts[c.Spec.Registry.Port]; reserved {
			return fmt.Errorf("spec.registry.port conflicts with a Kubernetes control-plane port")
		}
	}
	if strings.TrimSpace(c.Spec.SSH.User) == "" {
		return fmt.Errorf("spec.ssh.user is required")
	}
	if err := validatePort(c.Spec.SSH.Port); err != nil {
		return fmt.Errorf("spec.ssh.port: %w", err)
	}
	if !filepath.IsAbs(c.Spec.SSH.PrivateKey) {
		return fmt.Errorf("spec.ssh.privateKey must be an absolute path")
	}
	if c.Spec.Registry.Enabled && c.Spec.Registry.Port == c.Spec.SSH.Port {
		return fmt.Errorf("spec.registry.port must not conflict with spec.ssh.port")
	}

	return nil
}

func validateClusterName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("is required")
	}
	if len(name) > 63 || !clusterNamePattern.MatchString(name) {
		return fmt.Errorf("must be a lowercase DNS label with at most 63 characters")
	}
	return nil
}

func validateAdvertiseAddress(value string) error {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return fmt.Errorf("must be an IPv4 address")
	}
	if !isUsableUnicast(address) {
		return fmt.Errorf("must be a usable unicast address")
	}
	return nil
}

func validateEndpoint(value string) error {
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("must use host:port format")
	}
	if host == "" || (!validHostname(host) && net.ParseIP(host) == nil) {
		return fmt.Errorf("contains an invalid host")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if !isUsableUnicast(address) {
			return fmt.Errorf("must use a usable unicast address")
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("contains an invalid port")
	}
	if err := validatePort(port); err != nil {
		return err
	}
	return nil
}

func validHostname(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	value = strings.TrimSuffix(value, ".")
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || !hostnameLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validateNetwork(network NetworkSpec, advertiseAddress string) error {
	podCIDR, err := netip.ParsePrefix(network.PodCIDR)
	if err != nil || !podCIDR.Addr().Is4() {
		return fmt.Errorf("spec.network.podCIDR must be a valid IPv4 CIDR")
	}
	if podCIDR != podCIDR.Masked() {
		return fmt.Errorf("spec.network.podCIDR must use its canonical network address")
	}
	serviceCIDR, err := netip.ParsePrefix(network.ServiceCIDR)
	if err != nil || !serviceCIDR.Addr().Is4() {
		return fmt.Errorf("spec.network.serviceCIDR must be a valid IPv4 CIDR")
	}
	if serviceCIDR != serviceCIDR.Masked() {
		return fmt.Errorf("spec.network.serviceCIDR must use its canonical network address")
	}
	if podCIDR.Overlaps(serviceCIDR) {
		return fmt.Errorf("spec.network.podCIDR must not overlap spec.network.serviceCIDR")
	}
	address, err := netip.ParseAddr(advertiseAddress)
	if err == nil && (podCIDR.Contains(address) || serviceCIDR.Contains(address)) {
		return fmt.Errorf("spec.controlPlane.advertiseAddress must not belong to a Pod or Service CIDR")
	}
	return nil
}

func isUsableUnicast(address netip.Addr) bool {
	limitedBroadcast := netip.AddrFrom4([4]byte{255, 255, 255, 255})
	return address.IsValid() &&
		!address.IsUnspecified() &&
		!address.IsLoopback() &&
		!address.IsMulticast() &&
		address != limitedBroadcast
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("must be between 1 and 65535")
	}
	return nil
}
