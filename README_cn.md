# KubeLift

[English](README.md) | [简体中文](README_cn.md)

KubeLift 是一个用于在已有 Ubuntu 服务器上安装和运维 Kubernetes 集群的
命令行工具。KubeLift 运行在首个控制平面节点上，通过 SSH 管理其他节点，
使用预先准备的离线 Bundle 安装组件，使用 containerd 作为容器运行时，
并使用 Cilium 作为集群网络插件。

KubeLift 在安装集群时不会下载 Kubernetes 组件或容器镜像。所需的二进制
文件、运行时文件、镜像归档、清单和校验信息必须提前放入离线 Bundle。

## 功能概览

- 使用 `kubeadm` 在当前控制平面节点创建 Kubernetes v1.28.x 集群。
- 通过 SSH 添加其他控制平面节点和 Worker 节点。
- 从 Bundle 安装 containerd、kubelet、kubeadm、kubectl、宿主机工具和
  systemd 服务单元，不依赖操作系统软件包管理器。
- 将 Kubernetes、Cilium 和可选 Registry 镜像直接导入 containerd。
- 为 Docker Hub、GHCR 或其他 OCI 镜像仓库配置 containerd v2 镜像代理。
- 安装启用 kube-proxy replacement 的 Cilium。
- 在首个控制平面节点启动可选的 host-network Registry Pod。
- 持久化安装阶段，并支持安装中断后的显式恢复。
- 查询集群状态并清理 KubeLift 管理的集群资源。

## 架构说明

KubeLift 只需要安装在首个控制平面节点（Master0）上，其他节点不需要
安装 KubeLift 二进制文件。

```text
Master0
  ├── SSH -> 其他控制平面节点
  └── SSH -> Worker 节点
```

安装流程如下：

```text
离线 Bundle
      |
      v
宿主机准备 -> containerd 和 Kubernetes 二进制 -> 镜像导入
      |
      v
kubeadm init/join -> Cilium -> 健康检查 -> 可选 Registry
```

KubeLift 负责流程编排；控制平面初始化、证书生成、加入凭据创建和节点
加入仍由 `kubeadm` 完成。

## 支持范围

当前实现的支持边界如下：

| 项目 | 支持范围 |
| --- | --- |
| 操作系统 | Bundle 清单声明支持的 Ubuntu 版本 |
| CPU 架构 | `amd64`（`x86_64`）和 `arm64`（`aarch64`） |
| Kubernetes | v1.28.x；当前 kubeadm 配置生成器仅支持 v1.28 |
| 容器运行时 | 使用 systemd cgroup driver 的 containerd |
| CNI | 启用 kube-proxy replacement 的 Cilium |
| SSH 认证 | root 用户和私钥认证 |
| 密码认证 | 不支持 |
| Bundle | 每个 Bundle 对应一个精确 Kubernetes 版本和一种 CPU 架构 |

每台目标服务器必须运行 systemd，至少具有 2 个逻辑 CPU、约 1.8 GiB
内存，以及 `/var/lib` 下至少 10 GiB 可用空间。支持的 Ubuntu 版本和组件
精确版本由 Bundle 清单决定。

控制平面高可用要求在首次创建集群前配置稳定的
`spec.controlPlane.endpoint`。两个控制平面节点适合测试加入流程，但两个
成员的 etcd 不具备故障容错能力；具备控制平面容错能力的 stacked-etcd
集群至少需要三个控制平面节点。

## 构建 KubeLift

开发和发布构建需要 Go 1.26.1 或更高版本。

构建 Linux amd64 二进制文件：

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o kubelift .
```

面向 arm64 服务器时，将 `GOARCH=amd64` 改为 `GOARCH=arm64`。构建完成后，
将二进制文件和匹配的离线 Bundle 传输到 Master0。其他节点不需要单独安装
KubeLift。

## 配置文件

默认配置文件路径为 `/etc/kubelift/cluster.yaml`。配置模板生成命令不会
覆盖已存在的文件：

```bash
sudo kubelift config init
```

以下示例适用于 Kubernetes v1.28 的三节点集群。当前 CLI 的新增节点地址
通过 `add master` 和 `add node` 命令传入。

```yaml
apiVersion: kubelift.io/v1alpha1
kind: Cluster

metadata:
  name: production

spec:
  kubernetes:
    version: v1.28.15

  controlPlane:
    advertiseAddress: 192.168.121.151
    endpoint: 192.168.121.151:6443

  network:
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12

  offline:
    bundle: /opt/kubelift/kubernetes-v1.28.15-amd64.tar.zst

  registry:
    enabled: true
    port: 5000
    mirrors:
      docker.io: https://docker.m.daocloud.io
      ghcr.io: https://ghcr.example.com

  ssh:
    user: root
    port: 22
    privateKey: /root/.ssh/kubelift_ed25519
```

配置规则如下：

- `spec.kubernetes.version` 必须是 `v1.28.15` 形式的完整版本号。
- `spec.offline.bundle` 和 `spec.ssh.privateKey` 必须使用绝对路径。
- `spec.controlPlane.advertiseAddress` 是 Master0 的节点通信地址。
- 添加其他控制平面节点前必须配置稳定的
  `spec.controlPlane.endpoint`，该地址在集群生命周期内应保持不变。
- `spec.registry.enabled` 控制可选的本地 Registry Pod，不会自动把所有
  上游镜像仓库配置为镜像代理。
- `spec.registry.mirrors` 将源镜像仓库映射到 HTTP 或 HTTPS OCI 镜像代理。
  KubeLift 会为每个源仓库生成独立的 containerd v2 `hosts.toml`，并设置
  `registry.config_path`。
- 旧版 `spec.registry.mirror` 字段仍兼容，可作为 Docker Hub 镜像代理的
  快捷配置。

镜像代理地址必须能被所有节点访问，并实现对应源仓库的 OCI pull/resolve
接口。GitHub Container Registry 的地址是 `ghcr.io`，不是 `github.com`。

只校验配置文件的结构和值：

```bash
kubelift config validate
kubelift config validate -f /path/to/cluster.yaml
```

生成 Kubernetes v1.28 的 kubeadm 初始化配置，不修改当前服务器：

```bash
kubelift config kubeadm
```

生成的配置使用 containerd，设置 `imagePullPolicy: Never`，跳过 kube-proxy
阶段，并让 kubelet 使用 systemd cgroup driver。

## SSH 前置条件

KubeLift 使用公钥认证和严格的 `known_hosts` 主机公钥校验，不启用密码和
keyboard-interactive 认证。默认的 `known_hosts` 路径位于配置私钥的同级
目录。例如，私钥为 `/root/.ssh/kubelift_ed25519` 时，默认路径为
`/root/.ssh/known_hosts`。

远程账户必须已经写入配置公钥。添加主机公钥前，应通过可信渠道核对指纹：

```bash
ssh-keyscan -H 192.168.121.152 192.168.121.153 \
  >> /root/.ssh/known_hosts
```

`ssh-keyscan` 只读取远程 SSH 服务公开的主机公钥，不负责用户登录认证，
也不会把用户公钥安装到远程服务器。KubeLift 会拒绝未知主机公钥或发生变化
的主机公钥。

## 标准集群安装流程

以下命令均在 Master0 上以 `root` 用户执行。执行 `create` 前，配置文件和
离线 Bundle 必须已经位于 Master0。

### 1. 本地校验

```bash
kubelift config validate
kubelift check
```

`config validate` 检查 YAML 结构和值。`check` 进一步检查本地操作系统、
CPU 架构、Bundle 校验和及兼容性、私钥、CPU、内存、磁盘和 systemd；两者
均为只读命令。

### 2. 创建 Master0

```bash
kubelift create --dry-run
kubelift create
```

`create` 内部执行以下阶段：

1. 本地预检。
2. Bundle staging 和校验和验证。
3. 宿主机准备，包括必要时处理 swap。
4. 安装并配置 containerd 和 kubelet。
5. 将离线镜像导入 containerd 的 `k8s.io` 命名空间。
6. 针对配置的 Kubernetes 版本执行 `kubeadm init`。
7. 安装 Cilium 并等待其就绪。
8. 启动可选的本地 Registry Pod 并等待其就绪。

首个控制平面节点提供 Kubernetes API Server、etcd、Controller Manager、
Scheduler、kubelet、containerd、Cilium 和 CoreDNS。

### 3. 添加控制平面节点

```bash
kubelift check ssh 192.168.121.152
kubelift add master 192.168.121.152
```

`add master` 通过 SSH 准备目标服务器，安装组件，导入所需镜像，在 Master0
创建短期 kubeadm 加入凭据，传输控制平面证书，执行
`kubeadm join --control-plane`，并等待 API Server 和 etcd 成员健康。

### 4. 添加 Worker 节点

```bash
kubelift check ssh 192.168.121.153
kubelift add node 192.168.121.153
```

`add node` 准备目标服务器，导入 Kubernetes 和 Cilium 镜像，在 Master0 创建
短期 bootstrap token，执行 `kubeadm join`，并等待节点和 Cilium Agent 就绪。

显式执行 `check ssh` 是可选的；两个 `add` 命令在修改远程服务器前都会执行
相同的远程预检。

### 5. 验证集群

```bash
kubelift status
kubelift status --details
```

详细状态报告包含 Kubernetes Nodes、Cilium Pods、CoreDNS Pods，以及启用时
的本地 Registry Pod。

## 恢复和状态文件

创建状态文件位于：

```text
/var/lib/kubelift/state/<cluster-name>.yaml
```

节点加入状态文件位于：

```text
/var/lib/kubelift/state/<cluster-name>-add-<role>-<address>.yaml
```

状态文件记录当前阶段、配置 SHA-256 和 Bundle SHA-256，不保存 bootstrap
token 或 certificate key。

操作中断后必须显式恢复：

```bash
kubelift create --resume
kubelift add master 192.168.121.152 --resume
kubelift add node 192.168.121.153 --resume
```

`--resume` 必须使用与中断操作相同的配置和目标节点。如果无法确认
`kubeadm init` 或 `kubeadm join` 的执行结果，KubeLift 会停止，不会自动重
复执行或调用 `kubeadm reset`。

## 卸载集群

预览具有破坏性的清理计划：

```bash
kubelift uninstall --dry-run
```

执行清理：

```bash
kubelift uninstall --force
```

卸载流程从 Kubernetes API 发现节点，先清理 Worker，再清理其他控制平面
节点，最后清理当前控制平面节点。清理内容包括 Kubernetes、Cilium、
containerd 数据、systemd 服务单元、KubeLift staging/state，以及可选的
本地 Registry 数据。

默认保留配置文件和离线 Bundle。以下命令会同时删除当前控制平面节点的
`/etc/kubelift`：

```bash
kubelift uninstall --force --purge-config
```

卸载不会恢复宿主机准备阶段修改过的 swap 配置。重新启用其他业务使用的
swap 前，应检查 `/etc/fstab.kubelift.bak`。

## 离线 Bundle

离线 Bundle 是由 KubeLift 使用的组装产物，不是 Kubernetes 官方发布的统一
离线包。Bundle 包含指定安装配置所需的 Kubernetes 二进制文件、containerd
运行时文件、宿主机工具和动态库、systemd 单元、镜像归档、Cilium 和
Registry 清单，以及带 SHA-256 校验和的 manifest。

Bundle 在目标集群外准备，并传输到 Master0。KubeLift 不会从互联网下载
Bundle 内容。

Bundle 源目录结构如下：

```text
bundle-source/
├── manifest.yaml
├── bin/          # kubeadm、kubelet、kubectl 等二进制文件
├── cri/          # containerd 和运行时归档
├── etc/          # containerd、kubelet 和 systemd 配置
├── images/       # containerd 可导入的镜像归档
├── manifests/    # Cilium 和可选 Registry 模板
├── scripts/      # 可选初始化脚本
└── system/       # 宿主机工具和动态库
    ├── bin/
    └── lib/
```

生成 Bundle 清单：

```bash
kubelift bundle manifest ./bundle-source \
  --name kubernetes-v1-28-15-amd64 \
  --kubernetes-version v1.28.15 \
  --architecture amd64 \
  --ubuntu-version 22.04 \
  --containerd-version v1.7.27 \
  --cilium-version v1.14.19 \
  --registry-version v2.8.3
```

创建压缩 Bundle：

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

检查 Bundle：

```bash
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst --files
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst \
  --config /etc/kubelift/cluster.yaml
```

带配置的检查会验证必需的二进制文件、宿主机工具、动态库、systemd 单元、
镜像归档、Cilium 模板、Kubernetes 版本，以及启用 Registry 时所需的
Registry 文件。SHA-256 能发现 Bundle 内容损坏或归档与清单不一致，但不能
单独证明 Bundle 来源可信；发布流程还应对 manifest 进行签名和验签。

`tar.zst` 是 Bundle 的传输格式，不是 Kubernetes 官方发布格式。只有在所有
Bundle 工具同时变更时，才可以修改文件扩展名。

上传前，KubeLift 会将目标节点的 `uname -m` 和 Ubuntu `VERSION_ID` 与
Bundle 清单比较。Master0 和所有受管节点必须匹配清单声明的架构和 Ubuntu
版本。

## 高级 Bundle 命令

以下命令暴露单独的节点准备阶段。标准的 `create`、`add master` 和
`add node` 流程会自动调用这些阶段；这些命令适用于诊断、分阶段准备和受控
恢复。

```bash
kubelift bundle push 192.168.121.152
kubelift bundle prepare 192.168.121.152
kubelift bundle import-images 192.168.121.152
```

- `bundle push` 将 Bundle 上传并校验到
  `/var/lib/kubelift/staging/<cluster-name>`，不安装任何组件。
- `bundle prepare` 安装二进制文件、宿主机工具、动态库、containerd 和
  systemd 单元，不执行 `kubeadm` 或安装 Cilium。
- `bundle import-images` 将 Kubernetes、Cilium 和可选 Registry 镜像导入
  containerd，不访问镜像仓库，也不执行 `kubeadm`。

## 命令参考

```text
kubelift
├── add
│   ├── master <IPv4>
│   └── node <IPv4>
├── bundle
│   ├── create <source-directory>
│   ├── inspect <bundle.tar.zst>
│   ├── import-images <IPv4>
│   ├── manifest <source-directory>
│   ├── prepare <IPv4>
│   └── push <IPv4>
├── check
│   └── ssh <IPv4>
├── config
│   ├── init
│   ├── kubeadm
│   └── validate
├── create
├── uninstall (alias: reset)
├── status
└── version
```

大多数命令默认读取 `/etc/kubelift/cluster.yaml`。支持其他配置文件路径的
命令使用 `-f` 或 `--config`。命令特有的 timeout、dry-run、目标节点和恢复
选项可通过 `kubelift <command> --help` 查看。

## 安全和运维说明

- SSH 主机公钥校验是强制的，未知主机公钥不会进入交互式确认。
- 私钥从配置路径读取，不会写入集群配置内容。
- Bundle 在本地安装和远程传输前都会进行校验和验证。
- 可选 Registry Pod 使用 hostNetwork 和本地宿主机存储，是本地镜像缓存
  端点，不会自动替代所有上游镜像仓库。
- `uninstall --force` 具有破坏性，必须显式确认。
- 当前实现面向受控的 Ubuntu 环境。生产部署前应在等同的网络、磁盘、SSH、
  操作系统和 Bundle 条件下完成验证。
