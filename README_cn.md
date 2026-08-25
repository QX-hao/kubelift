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
│   └── manifest <source-directory>
├── check
├── config
│   ├── init
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

检查配置和当前 Master0：

```bash
kubelift check -f /etc/kubelift/cluster.yaml
```

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
├── images/       # 可由 containerd 导入的镜像归档
├── manifests/    # 安装所需的 Kubernetes 清单
└── packages/     # Ubuntu 离线软件包
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
  --registry-version v2.8.0
```

生成清单后创建并立即复验离线包：

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

分发到 Master0 后可以独立检查：

```bash
kubelift bundle inspect /opt/kubelift/kubernetes-v1.28.15-amd64.tar.zst --files
```

`kubelift check` 也会完整读取离线包，并确认 SHA-256、Kubernetes 版本、CPU 架构和 Ubuntu 兼容范围。SHA-256 能发现内容损坏或与清单不一致，但如果攻击者同时替换离线包和清单，它不能证明文件来自可信发布者；发布阶段还需要增加清单签名。

当前清单只定义包结构和校验信息，还不能单独证明包内已经包含一次完整安装所需的所有文件。等 Ubuntu 实机验证确定 kubelet、kubeadm、containerd、Cilium、Registry 和系统镜像的最终载荷布局后，清单会增加必需的 artifact role 校验。

发布构建可通过链接参数写入版本信息：

```bash
go build -ldflags "-X github.com/QX-hao/kubelift/internal/buildinfo.Version=v0.1.0 -X github.com/QX-hao/kubelift/internal/buildinfo.Commit=<commit> -X github.com/QX-hao/kubelift/internal/buildinfo.Date=<date>" .
```

## 开发状态

`config init`、`config validate`、`check`、`bundle manifest`、`bundle create`、`bundle inspect`、`status` 和 `version` 已可用。`create`、`add node` 和 `add master` 已完成参数校验与 dry-run 工作流。真实的系统安装、SSH 执行、镜像导入和 `kubeadm` 操作将在 Ubuntu 测试环境中接入；在执行器启用前，不带 `--dry-run` 的变更命令会明确失败，不会修改服务器。
