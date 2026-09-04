package preflight

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

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
	processorCount     int
	memoryKB           func() (uint64, error)
	diskAvailableKB    func(string) (uint64, error)
	systemdPath        string
}

func CheckLocal(configuration config.Config) []Result {
	checker := localChecker{
		osReleasePath:      "/etc/os-release",
		operatingSystem:    runtime.GOOS,
		architecture:       runtime.GOARCH,
		interfaceAddresses: activeInterfaceAddresses,
		processorCount:     runtime.NumCPU(),
		memoryKB:           readMemoryKB,
		diskAvailableKB:    availableDiskKB,
		systemdPath:        "/run/systemd/system",
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
		c.checkCPU(),
		c.checkMemory(),
		c.checkDisk(),
		c.checkSystemd(),
	}
}

func (c localChecker) checkCPU() Result {
	result := Result{Name: "CPU"}
	if c.processorCount < minimumCPUCount {
		result.Err = fmt.Errorf("requires at least %d logical processors, detected %d", minimumCPUCount, c.processorCount)
		return result
	}
	result.Detail = fmt.Sprintf("%d logical processors", c.processorCount)
	return result
}

func (c localChecker) checkMemory() Result {
	result := Result{Name: "memory"}
	if c.memoryKB == nil {
		result.Err = fmt.Errorf("memory checker is unavailable")
		return result
	}
	value, err := c.memoryKB()
	if err != nil {
		result.Err = err
		return result
	}
	if value < minimumMemoryKB {
		result.Err = fmt.Errorf("requires at least %d KiB, detected %d", minimumMemoryKB, value)
		return result
	}
	result.Detail = fmt.Sprintf("%d KiB", value)
	return result
}

func (c localChecker) checkDisk() Result {
	result := Result{Name: "disk"}
	if c.diskAvailableKB == nil {
		result.Err = fmt.Errorf("disk checker is unavailable")
		return result
	}
	value, err := c.diskAvailableKB("/var/lib")
	if err != nil {
		result.Err = err
		return result
	}
	if value < minimumDiskKB {
		result.Err = fmt.Errorf("requires at least %d KiB available under /var/lib, detected %d", minimumDiskKB, value)
		return result
	}
	result.Detail = fmt.Sprintf("%d KiB available under /var/lib", value)
	return result
}

func (c localChecker) checkSystemd() Result {
	result := Result{Name: "systemd"}
	info, err := os.Stat(c.systemdPath)
	if err != nil || !info.IsDir() {
		result.Err = fmt.Errorf("systemd is not running")
		return result
	}
	result.Detail = "running"
	return result
}

func readMemoryKB() (uint64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read system memory: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse system memory: %w", err)
			}
			return value, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read system memory: %w", err)
	}
	return 0, fmt.Errorf("MemTotal is missing from /proc/meminfo")
}

func availableDiskKB(path string) (uint64, error) {
	var statistics syscall.Statfs_t
	if err := syscall.Statfs(path, &statistics); err != nil {
		return 0, fmt.Errorf("read available disk space for %q: %w", path, err)
	}
	return uint64(statistics.Bavail) * uint64(statistics.Bsize) / 1024, nil
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

	values, err := parseOSRelease(file)
	if err != nil {
		return nil, fmt.Errorf("parse os-release %q: %w", path, err)
	}
	return values, nil
}

// parseOSRelease 解析本地或远程读取的 os-release 内容。
func parseOSRelease(reader io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid line %q", line)
		}
		decoded, err := decodeOSReleaseValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		values[strings.TrimSpace(key)] = decoded
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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
