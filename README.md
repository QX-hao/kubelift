# KubeLift

KubeLift is a Go-based CLI for bootstrapping Kubernetes clusters on existing Ubuntu servers. It is designed to run on the first control-plane node, use SSH to manage additional nodes, install from an offline bundle, use containerd as the container runtime, and install Cilium as the CNI.

The project is currently in the infrastructure and validation stage. Configuration validation, SSH preflight, offline bundle validation, and bundle staging are available. The complete node preparation and Kubernetes installation workflow is still being implemented.

## Current Scope

KubeLift is intended to run on `k8s1` (Master0) and connect to other nodes over SSH:

```text
k8s1 (Master0)
  ├── SSH -> k8s2 (additional control plane)
  └── SSH -> k8s3 (worker)
```

The remote nodes do not need KubeLift installed. The current implementation expects root SSH access using a private key. Password authentication is not supported.

## Requirements

- Go 1.26 or newer for development and builds
- Linux for the `check` command
- Ubuntu targets
- `amd64` (`x86_64`) or `arm64` (`aarch64`) targets
- Root SSH access from Master0 to every remote node
- A prepared offline bundle for the selected Kubernetes version

Before using the SSH commands, add the remote host keys to the `known_hosts` file next to the configured private key. KubeLift rejects unknown host keys and does not fall back to interactive confirmation.

## Build

Run tests and build a Linux amd64 binary:

```bash
go test ./...
go vet ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o kubelift .
```

For an arm64 target, use `GOARCH=arm64` instead.

## Configuration

Create the default configuration at `/etc/kubelift/cluster.yaml`:

```bash
sudo kubelift config init
```

The command does not overwrite an existing file. Edit the generated values before running a deployment command. The important fields are:

```yaml
spec:
  kubernetes:
    version: v1.28.15

  controlPlane:
    advertiseAddress: 192.168.121.151

  offline:
    bundle: /opt/kubelift/kubernetes-v1.28.15-amd64.tar.zst

  ssh:
    user: root
    port: 22
    privateKey: /root/.ssh/kubelift_ed25519
```

Validate only the configuration schema and values:

```bash
kubelift config validate
```

The default path is `/etc/kubelift/cluster.yaml`; use `-f` to select another file.

## Checks

Check the local Master0 host, private key, and offline bundle:

```bash
kubelift check
```

Check a remote node through SSH:

```bash
kubelift check ssh 192.168.121.152
kubelift check ssh 192.168.121.153
```

The remote check is read-only. It verifies the SSH connection, hostname, architecture, Ubuntu version, and that swap is disabled.

## Offline Bundles

KubeLift bundles are assembled artifacts, not a Kubernetes-provided universal archive. A bundle contains the exact binaries, runtime archives, images, manifests, configuration files, and initialization scripts required by a selected installation profile.

The source directory may contain these payload directories:

```text
bundle-source/
├── manifest.yaml
├── bin/
├── cri/
├── etc/
├── images/
├── manifests/
└── scripts/
```

Generate a manifest after placing payloads in the source directory:

```bash
kubelift bundle manifest ./bundle-source \
  --name kubernetes-v1-28-15-amd64 \
  --kubernetes-version v1.28.15 \
  --architecture amd64 \
  --ubuntu-version 22.04 \
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

Create the compressed bundle and verify it immediately:

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

Inspect a bundle independently:

```bash
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst --files
```

The bundle manifest records the Kubernetes version, supported Ubuntu versions, architecture, component versions, payload sizes, roles, and SHA-256 checksums. The archive uses `tar.zst` only as a transport format; it is not an official Kubernetes archive format.

## Staging

Upload every bundle payload to a remote node:

```bash
kubelift bundle push 192.168.121.152
```

Payloads are staged under:

```text
/var/lib/kubelift/staging/<cluster-name>/
```

`bundle push` only transfers and verifies the payloads. It does not install binaries, configure containerd, import images, run `kubeadm`, or install Cilium yet.

## Cluster Commands

The cluster commands currently generate validated plans only:

```bash
kubelift create --dry-run
kubelift add node 192.168.121.153 --dry-run
kubelift add master 192.168.121.152 --dry-run
```

Without `--dry-run`, these commands fail closed because the complete installation executor is not enabled yet. The planned executor will install prerequisites, import images into containerd, run `kubeadm init` on Master0, run `kubeadm join` on additional nodes, and install Cilium.

Adding a second control-plane node requires a stable `controlPlane.endpoint` in the configuration before cluster creation. A single control-plane cluster created without a stable endpoint cannot later be converted into a highly available control plane by `kubeadm` alone.

## Development Status

Available commands:

```text
kubelift
├── add
│   ├── master <IPv4>
│   └── node <IPv4>
├── bundle
│   ├── create <source-directory>
│   ├── inspect <bundle.tar.zst>
│   ├── manifest <source-directory>
│   └── push <IPv4>
├── check
│   └── ssh <IPv4>
├── config
│   ├── init
│   └── validate
├── create
├── status
└── version
```

The next implementation stage is to add binary and runtime preparation, systemd setup, image import, and then connect the staged artifacts to `kubeadm init` and `kubeadm join`.
