# KubeLift

KubeLift 是一个运行在首个控制平面节点上的 Kubernetes 集群部署 CLI。它通过 SSH 管理其他 Ubuntu 节点，使用 containerd、kubeadm 和 Cilium，并以离线安装作为首个实现目标。

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

SSH 检查使用配置中的私钥和同目录下的 `known_hosts`，只启用公钥认证，不会提示输入密码；连接成功后还会只读检查远程主机名、架构、Ubuntu 版本和 swap。首次连接前，请先用相同私钥手动连接并确认远程主机指纹。

查看创建和扩容计划：

```bash
kubelift create -f /etc/kubelift/cluster.yaml --dry-run
kubelift add node 10.0.0.21 -f /etc/kubelift/cluster.yaml --dry-run
kubelift add master 10.0.0.11 -f /etc/kubelift/cluster.yaml --dry-run
```

查询已有集群的节点状态：

```bash
kubelift status --kubeconfig /etc/kubernetes/admin.conf
```

查看 CLI 版本和构建信息：

```bash
kubelift version
kubelift --version
```

## 离线包

离线包使用 `tar.zst` 格式，根目录必须包含 `manifest.yaml`。清单声明 Kubernetes 版本、CPU 架构、兼容的 Ubuntu 版本、组件版本，以及每个载荷文件的大小和 SHA-256。生成结果示例见 `examples/bundle-manifest.yaml`。

准备源目录时，只能使用下面四类载荷目录：

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
  --containerd-version v1.7.0 \
  --cilium-version v1.14.0 \
  --registry-version v2.8.0 \
  --artifact-role bin/kubeadm=kubeadm \
  --artifact-role bin/kubelet=kubelet \
  --artifact-role bin/kubectl=kubectl \
  --artifact-role cri/containerd.tar.gz=containerd \
  --artifact-role bin/runc=runc \
  --artifact-role etc/systemd/containerd.service=systemd-unit \
  --artifact-role etc/systemd/kubelet.service=systemd-unit \
  --artifact-role etc/containerd/config.toml=containerd-config \
  --artifact-role scripts/init.sh=init-script \
  --artifact-role images/kubernetes.tar=kubernetes-image \
  --artifact-role images/cilium.tar=cilium-image
```

`--artifact-role` 可以重复使用，格式是 `载荷相对路径=角色`。支持的角色包括 `kubeadm`、`kubelet`、`kubectl`、`containerd`、`runc`、`systemd-unit`、`containerd-config`、`kubelet-config`、`init-script`、`cni-plugin`、`cri-tool`、`kubernetes-image`、`cilium-image`、`registry-image`、`cilium-manifest` 和 `registry-manifest`。角色目前允许为空，以兼容早期 Bundle；真正安装前应为安装所需载荷补齐角色。

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

当前清单定义载荷结构和校验信息。`bundle prepare` 会校验并使用必需的裸二进制、containerd runtime、配置和 systemd 载荷，但还不会导入镜像或初始化 Kubernetes 集群。等 Ubuntu 实机验证确定 Cilium、Registry 和系统镜像的最终载荷布局后，清单会增加更严格的必需角色校验。

发布构建可通过链接参数写入版本信息：

```bash
go build -ldflags "-X github.com/QX-hao/kubelift/internal/buildinfo.Version=v0.1.0 -X github.com/QX-hao/kubelift/internal/buildinfo.Commit=<commit> -X github.com/QX-hao/kubelift/internal/buildinfo.Date=<date>" .
```

## 开发状态

`config init`、`config validate`、`config kubeadm`、`check`、`check ssh`、`bundle manifest`、`bundle create`、`bundle inspect`、`bundle push`、`bundle prepare`、`bundle import-images`、`status` 和 `version` 已可用。内部 Master0 执行器已经可以完成本地 staging、节点准备、镜像导入和 `kubeadm init` 调用；公开的 `create` 会等 Cilium 安装与集群健康检查接入后再启用。在此之前，不带 `--dry-run` 的 `create` 和 `add` 命令仍会明确失败，不会修改服务器。
