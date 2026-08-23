package preflight

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/QX-hao/kubelift/internal/bundle"
	"github.com/QX-hao/kubelift/internal/config"
)

var supportedUbuntuVersions = map[string]struct{}{
	"22.04": {},
	"24.04": {},
	"26.04": {},
}

var supportedArchitectures = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

type Result struct {
	Name   string
	Detail string
	Err    error
}

type localChecker struct {
	osReleasePath      string
	operatingSystem    string
	architecture       string
	interfaceAddresses func() ([]netip.Addr, error)
}

func CheckLocal(configuration config.Config) []Result {
	checker := localChecker{
		osReleasePath:      "/etc/os-release",
		operatingSystem:    runtime.GOOS,
		architecture:       runtime.GOARCH,
		interfaceAddresses: activeInterfaceAddresses,
	}
	return checker.check(configuration)
}

func (c localChecker) check(configuration config.Config) []Result {
	return []Result{
		c.checkOperatingSystem(),
		c.checkArchitecture(),
		c.checkAdvertiseAddress(configuration.Spec.ControlPlane.AdvertiseAddress),
		c.checkOfflineBundle(configuration),
		checkRegularFile("SSH private key", configuration.Spec.SSH.PrivateKey, true),
	}
}

func (c localChecker) checkOperatingSystem() Result {
	result := Result{Name: "operating system"}
	if c.operatingSystem != "linux" {
		result.Err = fmt.Errorf("requires Linux, detected %s", c.operatingSystem)
		return result
	}

	values, err := readOSRelease(c.osReleasePath)
	if err != nil {
		result.Err = err
		return result
	}
	if values["ID"] != "ubuntu" {
		result.Err = fmt.Errorf("requires Ubuntu, detected %q", values["ID"])
		return result
	}
	version := values["VERSION_ID"]
	if _, supported := supportedUbuntuVersions[version]; !supported {
		result.Err = fmt.Errorf("unsupported Ubuntu version %q", version)
		return result
	}

	result.Detail = "Ubuntu " + version
	return result
}

func (c localChecker) checkArchitecture() Result {
	result := Result{Name: "architecture"}
	uname, supported := supportedArchitectures[c.architecture]
	if !supported {
		result.Err = fmt.Errorf("unsupported architecture %q", c.architecture)
		return result
	}

	result.Detail = fmt.Sprintf("%s (%s)", c.architecture, uname)
	return result
}

func (c localChecker) checkAdvertiseAddress(value string) Result {
	result := Result{Name: "advertise address"}
	want, err := netip.ParseAddr(value)
	if err != nil {
		result.Err = fmt.Errorf("parse configured address: %w", err)
		return result
	}

	addresses, err := c.interfaceAddresses()
	if err != nil {
		result.Err = fmt.Errorf("list active interface addresses: %w", err)
		return result
	}
	for _, address := range addresses {
		if address.Unmap() == want.Unmap() {
			result.Detail = value
			return result
		}
	}

	result.Err = fmt.Errorf("%s is not assigned to an active local interface", value)
	return result
}

func (c localChecker) checkOfflineBundle(configuration config.Config) Result {
	result := Result{Name: "offline bundle"}
	report, err := bundle.Inspect(configuration.Spec.Offline.Bundle)
	if err != nil {
		result.Err = err
		return result
	}
	manifest := report.Manifest
	if manifest.Spec.KubernetesVersion != configuration.Spec.Kubernetes.Version {
		result.Err = fmt.Errorf(
			"bundle Kubernetes version is %s, configuration requires %s",
			manifest.Spec.KubernetesVersion,
			configuration.Spec.Kubernetes.Version,
		)
		return result
	}
	if manifest.Spec.Architecture != c.architecture {
		result.Err = fmt.Errorf("bundle architecture is %s, current host is %s", manifest.Spec.Architecture, c.architecture)
		return result
	}

	// 操作系统检查已经负责报告读取错误；这里只在能够识别 Ubuntu 时校验兼容列表。
	if values, err := readOSRelease(c.osReleasePath); err == nil && values["ID"] == "ubuntu" {
		version := values["VERSION_ID"]
		compatible := false
		for _, supported := range manifest.Spec.UbuntuVersions {
			if supported == version {
				compatible = true
				break
			}
		}
		if !compatible {
			result.Err = fmt.Errorf("bundle does not support Ubuntu %s", version)
			return result
		}
	}

	result.Detail = fmt.Sprintf("%s (%d files, %d bytes verified)", configuration.Spec.Offline.Bundle, report.FileCount, report.TotalSize)
	return result
}

func checkRegularFile(name, path string, private bool) Result {
	result := Result{Name: name}
	info, err := os.Stat(path)
	if err != nil {
		result.Err = fmt.Errorf("stat %q: %w", path, err)
		return result
	}
	if !info.Mode().IsRegular() {
		result.Err = fmt.Errorf("%q is not a regular file", path)
		return result
	}
	if info.Size() == 0 {
		result.Err = fmt.Errorf("%q is empty", path)
		return result
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		result.Err = fmt.Errorf("%q permissions must not allow group or other access", path)
		return result
	}

	file, err := os.Open(path)
	if err != nil {
		result.Err = fmt.Errorf("open %q: %w", path, err)
		return result
	}
	if err := file.Close(); err != nil {
		result.Err = fmt.Errorf("close %q: %w", path, err)
		return result
	}

	result.Detail = path
	return result
}

func readOSRelease(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open os-release %q: %w", path, err)
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("parse os-release %q: invalid line %q", path, line)
		}
		decoded, err := decodeOSReleaseValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("parse os-release %q key %q: %w", path, key, err)
		}
		values[strings.TrimSpace(key)] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read os-release %q: %w", path, err)
	}
	return values, nil
}

func decodeOSReleaseValue(value string) (string, error) {
	if strings.HasPrefix(value, "\"") {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return decoded, nil
	}
	if strings.HasPrefix(value, "'") {
		if len(value) < 2 || !strings.HasSuffix(value, "'") {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	return value, nil
}

func activeInterfaceAddresses() ([]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var addresses []netip.Addr
	for _, networkInterface := range interfaces {
		// 未启用的网卡不能作为 kube-apiserver 或 Registry 的对外地址。
		if networkInterface.Flags&net.FlagUp == 0 {
			continue
		}
		interfaceAddresses, err := networkInterface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, interfaceAddress := range interfaceAddresses {
			prefix, err := netip.ParsePrefix(interfaceAddress.String())
			if err == nil {
				addresses = append(addresses, prefix.Addr())
			}
		}
	}
	return addresses, nil
}
