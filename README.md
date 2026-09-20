# KubeLift

[English](README.md) | [简体中文](README_cn.md)

KubeLift is a Go command-line tool for installing and operating Kubernetes
clusters on existing Ubuntu servers. It runs on the first control-plane node,
uses SSH to manage additional nodes, installs from a prepared offline Bundle,
uses containerd as the container runtime, and installs Cilium as the cluster
network plugin.

KubeLift does not download Kubernetes components or container images while a
cluster is being installed. The required binaries, runtime files, image
archives, manifests, and checksums must be supplied in the offline Bundle.

## Features

- Create a Kubernetes v1.28.x cluster on the current control-plane node with
  `kubeadm`.
- Add additional control-plane and worker nodes over SSH.
- Install containerd, kubelet, kubeadm, kubectl, host tools, and systemd units
  from the Bundle without using distribution package managers.
- Import Kubernetes, Cilium, and optional Registry images directly into
  containerd.
- Configure containerd v2 registry mirrors for Docker Hub, GHCR, or other OCI
  registries.
- Install Cilium with kube-proxy replacement enabled.
- Start an optional host-network Registry Pod on the first control-plane node.
- Persist installation phases and support explicit recovery after interruption.
- Report cluster status and remove KubeLift-managed cluster state.

## Architecture

KubeLift is installed only on the first control-plane node (Master0). Remote
nodes do not need the KubeLift binary.

```text
Master0
  ├── SSH -> additional control-plane node
  └── SSH -> worker node
```

The installation workflow is:

```text
offline Bundle
      |
      v
host preparation -> containerd and Kubernetes binaries -> image import
      |
      v
kubeadm init/join -> Cilium -> readiness checks -> optional Registry
```

KubeLift orchestrates the workflow. `kubeadm` remains responsible for
initializing the control plane, generating certificates, creating join
credentials, and joining nodes.

## Supported Scope

The current implementation has the following boundaries:

| Item | Supported scope |
| --- | --- |
| Target OS | Ubuntu hosts supported by the Bundle manifest |
| CPU architecture | `amd64` (`x86_64`) and `arm64` (`aarch64`) |
| Kubernetes profile | v1.28.x; the kubeadm configuration generator is currently v1.28-only |
| Runtime | containerd with the systemd cgroup driver |
| CNI | Cilium with kube-proxy replacement |
| SSH authentication | Root user and private-key authentication only |
| Password authentication | Not supported |
| Bundle | One exact Kubernetes version and one CPU architecture per Bundle |

The target system must have systemd, at least 2 logical CPUs, approximately
1.8 GiB of memory, and at least 10 GiB available under `/var/lib`. The Bundle
manifest determines the supported Ubuntu versions and exact component
versions.

Control-plane high availability requires a stable
`spec.controlPlane.endpoint` configured before the first cluster creation.
Two control-plane nodes are suitable for join-path testing but do not provide
etcd fault tolerance; three control-plane nodes are required for a
fault-tolerant stacked-etcd control plane.

## Build

Development and release builds require Go 1.26.1 or newer.

Run tests and build a Linux amd64 binary:

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o kubelift .
```

For an arm64 target, replace `GOARCH=amd64` with `GOARCH=arm64`. The resulting
binary and the matching offline Bundle are transferred to Master0. The remote
nodes do not require a separate KubeLift installation.

## Configuration

The default configuration path is `/etc/kubelift/cluster.yaml`. The template
generator does not overwrite an existing file.

```bash
sudo kubelift config init
```

The following is a minimal v1.28 three-node configuration. The current CLI
accepts additional node addresses through `add master` and `add node`
commands.

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

Configuration rules:

- `spec.kubernetes.version` must be an exact version such as `v1.28.15`.
- `spec.offline.bundle` and `spec.ssh.privateKey` must be absolute paths.
- `spec.controlPlane.advertiseAddress` is the Master0 node address.
- `spec.controlPlane.endpoint` is required before adding another control-plane
  node and must remain stable for the lifetime of the cluster.
- `spec.registry.enabled` controls the optional local Registry Pod. It does
  not configure a pull-through mirror automatically.
- `spec.registry.mirrors` maps an upstream registry name to an HTTP or HTTPS
  OCI mirror. KubeLift writes one containerd v2 `hosts.toml` file per source
  registry and sets `registry.config_path`.
- The legacy `spec.registry.mirror` field remains supported as a Docker Hub
  mirror shortcut.

The mirror endpoint must be reachable from every node and implement the OCI
pull/resolve API for the corresponding source registry. `ghcr.io` is the GitHub
Container Registry hostname; `github.com` is not an image registry endpoint.

Validate the configuration without checking the host or Bundle:

```bash
kubelift config validate
kubelift config validate -f /path/to/cluster.yaml
```

Render the Kubernetes v1.28 kubeadm initialization configuration without
changing the host:

```bash
kubelift config kubeadm
```

The generated configuration uses containerd, sets `imagePullPolicy: Never`,
skips the kube-proxy phase for Cilium replacement, and configures the systemd
cgroup driver.

## SSH Prerequisites

KubeLift uses public-key authentication and strict `known_hosts` validation.
Password and keyboard-interactive authentication are disabled. The default
known-hosts path is next to the configured private key. For example, a private
key at `/root/.ssh/kubelift_ed25519` uses `/root/.ssh/known_hosts`.

The remote account must already authorize the configured public key. Host keys
must be obtained and verified through a trusted channel before they are added:

```bash
ssh-keyscan -H 192.168.121.152 192.168.121.153 \
  >> /root/.ssh/known_hosts
```

`ssh-keyscan` reads the SSH service's public host keys; it does not authenticate
the user and does not install the user's public key. KubeLift rejects an
unknown host key or a host-key change.

## Standard Cluster Workflow

All commands below run on Master0 as `root`, unless stated otherwise. The
configuration and Bundle must be present on Master0 before `create` runs.

### 1. Local validation

```bash
kubelift config validate
kubelift check
```

`config validate` checks the YAML schema and values. `check` additionally
checks the local OS, architecture, Bundle checksums and compatibility, private
key, CPU, memory, disk, and systemd. Both commands are read-only.

### 2. Create Master0

```bash
kubelift create --dry-run
kubelift create
```

The create workflow performs the following phases internally:

1. Local preflight checks.
2. Bundle staging and checksum verification.
3. Host preparation, including swap handling when required.
4. Installation and configuration of containerd and kubelet.
5. Offline image import into containerd's `k8s.io` namespace.
6. `kubeadm init` for the configured Kubernetes version.
7. Cilium installation and readiness checks.
8. Optional local Registry Pod startup and readiness checks.

The first control-plane node provides the Kubernetes API server, etcd,
controller manager, scheduler, kubelet, containerd, Cilium, and CoreDNS.

### 3. Add control-plane nodes

```bash
kubelift check ssh 192.168.121.152
kubelift add master 192.168.121.152
```

`add master` prepares the target over SSH, installs the required components,
imports the required images, creates short-lived kubeadm join credentials on
Master0, transfers the control-plane certificates, runs `kubeadm join
--control-plane`, and waits for the API server and etcd member to become
healthy.

### 4. Add worker nodes

```bash
kubelift check ssh 192.168.121.153
kubelift add node 192.168.121.153
```

`add node` prepares the target, imports Kubernetes and Cilium images, creates a
short-lived bootstrap token on Master0, runs `kubeadm join`, and waits for the
node and Cilium agent to become ready.

The explicit `check ssh` commands are optional; both add commands perform the
same required remote preflight checks before making changes.

### 5. Verify the cluster

```bash
kubelift status
kubelift status --details
```

The detailed status report includes Kubernetes Nodes, Cilium Pods, CoreDNS
Pods, and the optional local Registry Pod.

## Recovery and State

Create state is stored at:

```text
/var/lib/kubelift/state/<cluster-name>.yaml
```

Node-addition state is stored at:

```text
/var/lib/kubelift/state/<cluster-name>-add-<role>-<address>.yaml
```

State records the current phase, the configuration SHA-256, and the Bundle
SHA-256. It does not store bootstrap tokens or certificate keys.

After an interruption, KubeLift requires explicit recovery:

```bash
kubelift create --resume
kubelift add master 192.168.121.152 --resume
kubelift add node 192.168.121.153 --resume
```

The `--resume` argument must use the same configuration and target as the
interrupted operation. If the result of `kubeadm init` or `kubeadm join` cannot
be determined, KubeLift stops instead of rerunning the command or invoking
`kubeadm reset` automatically.

## Uninstall

Preview the destructive cleanup plan:

```bash
kubelift uninstall --dry-run
```

Execute cleanup:

```bash
kubelift uninstall --force
```

The workflow discovers nodes from the Kubernetes API, resets workers first,
resets secondary control-plane nodes, and resets the current control-plane
node last. It removes Kubernetes, Cilium, containerd data, systemd units,
KubeLift staging/state, and optional local Registry data from managed nodes.

The configuration and offline Bundle are preserved by default. The following
option also removes `/etc/kubelift` from the current control-plane node:

```bash
kubelift uninstall --force --purge-config
```

Uninstall does not restore swap entries changed during host preparation.
Review `/etc/fstab.kubelift.bak` before re-enabling swap for other workloads.

## Offline Bundles

An offline Bundle is an assembled KubeLift artifact, not an archive published
by Kubernetes. It contains the exact files required by the selected profile:
Kubernetes binaries, containerd runtime files, host tools and libraries,
systemd units, image archives, Cilium and Registry manifests, and a manifest
with SHA-256 checksums.

The Bundle is prepared outside the target cluster and transferred to Master0.
KubeLift does not download it from the Internet.

Expected source layout:

```text
bundle-source/
├── manifest.yaml
├── bin/          # kubeadm, kubelet, kubectl and related binaries
├── cri/          # containerd and runtime archives
├── etc/          # containerd, kubelet and systemd configuration
├── images/       # containerd-compatible image archives
├── manifests/    # Cilium and optional Registry templates
├── scripts/      # optional initialization scripts
└── system/       # host tools and shared libraries
    ├── bin/
    └── lib/
```

Generate a manifest:

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

Create the compressed Bundle:

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

Inspect and verify the Bundle:

```bash
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst --files
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst \
  --config /etc/kubelift/cluster.yaml
```

The configuration-aware inspection checks required binaries, host tools,
libraries, systemd units, image archives, Cilium template, Kubernetes version,
and optional Registry files. SHA-256 detects corruption or a mismatch between
the archive and its manifest. SHA-256 alone does not prove the origin of the
Bundle; a release process should also sign and verify the manifest.

The archive uses `tar.zst` as a transport format. It is not an official
Kubernetes distribution format. The file extension can be changed only if all
Bundle tooling is updated consistently.

Before upload, KubeLift compares the target's `uname -m` and Ubuntu
`VERSION_ID` with the Bundle manifest. Master0 and every managed node must
match the declared architecture and supported Ubuntu version.

## Advanced Bundle Commands

The following commands expose individual preparation stages. The normal
`create`, `add master`, and `add node` workflows call these stages internally.
They are intended for diagnostics, staging, and controlled recovery.

```bash
kubelift bundle push 192.168.121.152
kubelift bundle prepare 192.168.121.152
kubelift bundle import-images 192.168.121.152
```

- `bundle push` uploads and verifies payloads under
  `/var/lib/kubelift/staging/<cluster-name>`. It does not install anything.
- `bundle prepare` installs binaries, host tools, libraries, containerd, and
  systemd units. It does not run `kubeadm` or install Cilium.
- `bundle import-images` imports Kubernetes, Cilium, and optionally Registry
  images into containerd. It does not pull from a registry or run `kubeadm`.

## Command Reference

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

Most commands use `/etc/kubelift/cluster.yaml`. Commands that support another
configuration path accept `-f` or `--config`. Use `kubelift <command> --help`
for command-specific timeout, target, dry-run, and recovery options.

## Security and Operational Notes

- SSH host-key verification is mandatory; unknown keys are not accepted
  interactively.
- Private keys are read from the configured path and are not written into the
  cluster configuration contents.
- Bundle payloads are checksum-verified before installation and remote
  transfer.
- The optional Registry Pod uses host networking and local host storage. It is
  a local cache endpoint, not an automatic replacement for every upstream
  registry.
- `uninstall --force` is destructive and must be explicitly confirmed.
- The current implementation is intended for controlled Ubuntu environments.
  Staging validation of the exact Bundle, network, disk, and SSH environment
  is required before production deployment.
