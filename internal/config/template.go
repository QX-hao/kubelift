package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Template = `apiVersion: kubelift.io/v1alpha1
kind: Cluster

metadata:
  name: production

spec:
  kubernetes:
    # 必须填写完整版本号，不能使用 latest 或 v1.28 这样的模糊版本。
    version: v1.28.15

  controlPlane:
    # 当前 Master0 在集群节点间通信使用的 IPv4 地址。
    advertiseAddress: 10.0.0.10
    # 如果以后需要添加 Master，必须在首次创建集群前配置稳定入口。
    # endpoint: 10.0.0.100:6443

  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12

  offline:
    # 离线包必须使用绝对路径。
    bundle: /opt/kubelift/kubernetes-v1.28.15-linux-amd64.tar.zst

  registry:
    enabled: true
    port: 5000

  ssh:
    user: root
    port: 22
    # 这里只填写私钥路径，不要把私钥内容写进配置文件。
    privateKey: /root/.ssh/id_ed25519
`

func WriteTemplate(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("configuration output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}

	// 使用 O_EXCL，避免误覆盖服务器上已经生效的集群配置。
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("cluster configuration %q already exists", path)
		}
		return fmt.Errorf("create cluster configuration %q: %w", path, err)
	}

	if _, err := file.WriteString(Template); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write cluster configuration %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close cluster configuration %q: %w", path, err)
	}

	return nil
}
