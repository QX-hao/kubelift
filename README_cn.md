# KubeLift

KubeLift 是一个运行在首个控制平面节点上的 Kubernetes 集群部署 CLI。它通过 SSH 管理其他 Ubuntu 节点，使用 containerd、kubeadm 和 Cilium，并以离线安装作为首个实现目标。

当前每个节点至少需要 2 个逻辑 CPU、约 1.8 GiB 内存、`/var/lib` 下 10 GiB 可用空间，并且必须由 systemd 管理系统服务。你准备的 4C4G、2C2G、2C2G 和每台 50G 磁盘满足这组预检阈值。

## 命令树

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
├── config
│   ├── init
│   ├── kubeadm
│   └── validate
├── create
├── status
└── version
```

`add`、`bundle` 和 `config` 是命令分组，本身用于承载下一级子命令。命令后的参数和选项由具体子命令定义；例如，`config init` 用于生成配置模板，`bundle create` 用于生成离线包。

## 当前命令

生成默认配置：

```bash
sudo kubelift config init
```

默认配置路径为 `/etc/kubelift/cluster.yaml`。也可以使用 `-o` 指定其他输出路径；已有文件不会被覆盖。

只校验配置语法和字段值，不检查当前服务器：

```bash
kubelift config validate -f /etc/kubelift/cluster.yaml
```

只生成 Kubernetes v1.28 的 kubeadm 初始化配置，不修改服务器：

```bash
kubelift config kubeadm
```

生成的多文档 YAML 会指定 containerd、禁止联网拉取镜像、跳过 kube-proxy 阶段，并让 kubelet 使用 systemd cgroup。其他 Kubernetes 次版本会明确拒绝，直到实现对应的 kubeadm 配置 API。

检查配置和当前 Master0：

```bash
kubelift check -f /etc/kubelift/cluster.yaml
```

检查 Master0 到远程节点的 SSH：

```bash
kubelift check ssh 192.168.121.152 -f /etc/kubelift/cluster.yaml
kubelift check ssh 192.168.121.153 -f /etc/kubelift/cluster.yaml
```

SSH 检查使用配置中的私钥和同目录下的 `known_hosts`，只启用公钥认证，不会提示输入密码；连接成功后还会只读检查远程主机名、架构、Ubuntu 版本、CPU、内存、磁盘空间、systemd、swap 以及 Bundle 兼容性。首次连接前，请先用相同私钥手动连接并确认远程主机指纹。

查看创建和扩容计划：

```bash
kubelift create -f /etc/kubelift/cluster.yaml --dry-run
kubelift add node 10.0.0.21 -f /etc/kubelift/cluster.yaml --dry-run
kubelift add master 10.0.0.11 -f /etc/kubelift/cluster.yaml --dry-run
```

在 Master0 上创建集群；如果执行中断，检查现场后显式恢复：

```bash
kubelift create -f /etc/kubelift/cluster.yaml
kubelift create -f /etc/kubelift/cluster.yaml --resume
```

创建进度保存在 `/var/lib/kubelift/state/<cluster>.yaml`，并绑定当前配置和 Bundle 的 SHA-256。状态不一致或 `kubeadm init` 处于无法确定的中间状态时，KubeLift 会停止，不会自动执行 `kubeadm reset` 或重复初始化。

通过 SSH 添加 Worker：

```bash
kubelift add node 192.168.121.153 -f /etc/kubelift/cluster.yaml
```

真实执行目前要求 SSH 用户为 `root`。命令会确认目标尚未加入集群，上传并校验 Bundle，准备 containerd 和 kubelet，直接导入 Kubernetes 与 Cilium 镜像，在 Master0 创建两小时有效的 kubeadm bootstrap token，上传生成的 `JoinConfiguration`，最后等待节点 Ready。Worker 不会导入 Registry 镜像。

首次执行并修改远端节点前，`add node` 会确认 `10250` 端口未被占用；`add master` 会确认 `2379`、`2380`、`6443`、`10250`、`10257` 和 `10259` 均未被占用。端口被占用时命令会直接停止，不会继续安装。`--resume` 会跳过此项检查，因为前一次 `kubeadm join` 可能已经启动相关服务；后续工作由阶段状态文件控制。

通过 SSH 添加控制平面节点：

```bash
kubelift add master 192.168.121.152 -f /etc/kubelift/cluster.yaml
```

`add master` 同样要求 SSH 用户为 `root`，并要求首次创建集群前已经配置稳定的 `controlPlane.endpoint`。它会使用短期 certificate key 上传控制面证书，生成控制面 `JoinConfiguration`，加入后等待新节点、API Server 和 etcd 静态 Pod Ready。两个 Master 可以测试加入流程，但两成员 etcd 不能容忍任意一个成员故障；真正的控制面容错需要三个 Master。

节点扩容进度保存在 `/var/lib/kubelift/state/<cluster>-add-<role>-<address>.yaml`。中断后使用相同参数并增加 `--resume` 继续；状态文件不会保存 bootstrap token 或 certificate key。如果无法确认 `kubeadm join` 是否完成，KubeLift 会停止，不会自动重跑 join 或执行 `kubeadm reset`。

查询已有集群的节点状态：

```bash
kubelift status --kubeconfig /etc/kubernetes/admin.conf
kubelift status --details -f /etc/kubelift/cluster.yaml
```

`--details` 是只读检查，会显示 Node、Cilium、CoreDNS，以及配置启用时的 host-network Registry；查询失败时会指出具体组件并保留 kubectl 的错误输出。

查看 CLI 版本和构建信息：

```bash
kubelift version
kubelift --version
```

## 离线包

离线包使用 `tar.zst` 格式，根目录必须包含 `manifest.yaml`。清单声明 Kubernetes 版本、CPU 架构、兼容的 Ubuntu 版本、组件版本，以及每个载荷文件的大小和 SHA-256。生成结果示例见 `examples/bundle-manifest.yaml`。

准备源目录时，只能使用下面六类载荷目录：

```text
bundle-source/
├── manifest.yaml
├── bin/          # 可执行文件
├── cri/          # containerd 等运行时压缩包
├── etc/          # containerd、kubelet 和 systemd 配置
├── images/       # 可由 containerd 导入的镜像归档
├── manifests/    # 安装所需的 Kubernetes 清单
└── scripts/      # 节点初始化脚本
```

准备好载荷后，由 CLI 扫描目录并生成 `manifest.yaml`。命令不会覆盖已有清单：

```bash
kubelift bundle manifest ./bundle-source \
  --name kubernetes-v1-28-15-amd64 \
  --kubernetes-version v1.28.15 \
  --architecture amd64 \
  --ubuntu-version 22.04,24.04,26.04 \
  --containerd-version v1.7.27 \
  --cilium-version v1.14.19 \
  --registry-version v2.8.3
```

Cilium manifest 使用 Go 文本模板，使同一个 Bundle 可以用于不同集群。模板中的 Cilium API Server 配置必须包含：

```yaml
k8s-service-host: "{{ .APIServerHost }}"
k8s-service-port: "{{ .APIServerPort }}"
```

模板还可以选择使用 `{{ .PodCIDR }}` 和 `{{ .ClusterName }}`。

当 `registry.enabled: true` 时，Registry 模板必须包含 `{{ .RegistryPort }}` 和 `{{ .RegistryStoragePath }}`，并且只能生成一个 `kube-system/kubelift-registry` 静态 Pod。该 Pod 必须使用 `hostNetwork`、`hostPath`、`app.kubernetes.io/name=kubelift-registry` 标签以及 `imagePullPolicy: Never`。

`--artifact-role` 可以重复使用，格式是 `载荷相对路径=角色`。支持的角色包括 `kubeadm`、`kubelet`、`kubectl`、`containerd`、`runc`、`systemd-unit`、`containerd-config`、`kubelet-config`、`init-script`、`cni-plugin`、`cri-tool`、`kubernetes-image`、`cilium-image`、`registry-image`、`cilium-manifest` 和 `registry-manifest`。约定路径会自动标注；非标准路径仍需显式指定。角色允许为空以容纳非安装载荷，但 `bundle inspect --config` 会拒绝缺少安装角色的 Bundle。

生成清单后创建并立即复验离线包：

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

分发到 Master0 后可以独立检查：

```bash
 kubelift bundle inspect /opt/kubelift/kubernetes-v1.28.15-amd64.tar.zst --files
```

将配置中的 Bundle 上传到远程节点的 KubeLift staging 目录：

```bash
kubelift bundle push 192.168.121.152
kubelift bundle push 192.168.121.153
```

`bundle push` 会先检查本机和远程节点，再通过 SSH 上传所有载荷，并在远程节点执行 SHA-256 复核。它只写入 `/var/lib/kubelift/staging/<cluster-name>`，不会安装二进制、配置 containerd、导入镜像或执行 `kubeadm`。

使用 Sealos 风格的 Bundle 载荷准备远程节点：

```bash
kubelift bundle prepare 192.168.121.152
```

该命令会上传并校验 Bundle，然后将 `kubeadm`、`kubelet`、`kubectl` 等裸二进制复制到 `/usr/bin`，加载所需内核模块并设置 Kubernetes 网络 sysctl，解压 containerd runtime，安装 containerd 和 kubelet 的 systemd 配置，并启用/重启 containerd。它暂不执行 `kubeadm`、导入镜像或安装 Cilium。

将 Kubernetes、Cilium 和可选 Registry 镜像归档导入远程节点的 containerd：

```bash
kubelift bundle import-images 192.168.121.152
```

该命令使用 `ctr -n k8s.io images import --all-platforms`，不会从镜像仓库拉取内容。目标节点必须先完成 `bundle prepare` 并且 containerd 正在运行。

`kubelift check` 也会完整读取离线包，并确认 SHA-256、Kubernetes 版本、CPU 架构和 Ubuntu 兼容范围。SHA-256 能发现内容损坏或与清单不一致，但如果攻击者同时替换离线包和清单，它不能证明文件来自可信发布者；发布阶段还需要增加清单签名。

当前清单定义载荷结构和校验信息。`bundle prepare` 会校验并使用必需的裸二进制、containerd runtime、配置和 systemd 载荷；内部 Master0 执行器还会要求 Cilium 模板，并在 `kubeadm init` 后渲染、应用和检查 Cilium。启用 Registry 时，还会要求 Registry 镜像和静态 Pod 模板；关闭时完整跳过缓存链路。

`bin/kubeadm`、`cri/containerd.tar.gz`、`images/kubernetes.tar`、`manifests/cilium.yaml.tmpl` 等约定路径会在生成清单时自动获得安装角色。只有额外文件或非标准文件名才需要使用 `--artifact-role path=role`，显式参数不能覆盖冲突的约定角色。

打包后可以结合集群配置检查完整安装契约：

```bash
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst \
  --config /etc/kubelift/cluster.yaml
```

该检查会验证必需的二进制、systemd unit、镜像归档、Cilium 模板、Kubernetes 版本，以及启用 Registry 时所需的 Registry 载荷。

任何远程上传开始前，KubeLift 都会把目标节点的 `uname -m` 和 Ubuntu `VERSION_ID` 与 Bundle 清单比较。一个 Bundle 只对应一种架构（`amd64` 或 `arm64`），但可以声明多个受支持的 Ubuntu 版本；Master0 和所有新增节点都必须与清单匹配。

发布构建可通过链接参数写入版本信息：

```bash
go build -ldflags "-X github.com/QX-hao/kubelift/internal/buildinfo.Version=v0.1.0 -X github.com/QX-hao/kubelift/internal/buildinfo.Commit=<commit> -X github.com/QX-hao/kubelift/internal/buildinfo.Date=<date>" .
```

## 开发状态

`config init`、`config validate`、`config kubeadm`、`check`、`check ssh`、`bundle manifest`、`bundle create`、`bundle inspect`、`bundle push`、`bundle prepare`、`bundle import-images`、`create`、`add node`、`add master`、`status` 和 `version` 已可用。`create` 可以完成本地 staging、节点准备、镜像导入、`kubeadm init`、Cilium 安装、API Server/节点/CoreDNS 健康检查以及可选 Registry 缓存启动，并支持基于阶段状态的显式恢复。`add node` 和 `add master` 已分别支持 Worker 与控制平面节点加入。
