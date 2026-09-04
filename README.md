# KubeLift

KubeLift is a Go-based CLI for bootstrapping Kubernetes clusters on existing Ubuntu servers. It is designed to run on the first control-plane node, use SSH to manage additional nodes, install from an offline bundle, use containerd as the container runtime, and install Cilium as the CNI.

The project now implements offline Master0 creation, worker and control-plane joins, Cilium installation, optional Registry startup, resumable phase state, and post-installation status checks. Real-host integration testing is still required before production use.

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
- At least 2 logical CPUs, approximately 1.8 GiB memory, and 10 GiB free under `/var/lib` on every node
- systemd running on every node

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

Render the Kubernetes v1.28 kubeadm init configuration without changing the
host:

```bash
kubelift config kubeadm
```

The generated multi-document YAML uses containerd, prevents image pulls,
skips the kube-proxy phase for Cilium replacement, and configures the kubelet
to use the systemd cgroup driver. Other Kubernetes minor versions are rejected
until their kubeadm configuration API is implemented.

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

The remote check is read-only. It verifies the SSH connection, hostname,
architecture, Ubuntu version, CPU, memory, free disk space, systemd, swap, and
Bundle compatibility.

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
  --containerd-version v1.7.27 \
  --cilium-version v1.14.19 \
  --registry-version v2.8.3
```

The conventional paths shown above are assigned their installation roles
automatically. Use `--artifact-role path=role` only for additional files or
non-standard names; an explicit role cannot override a conflicting conventional
role.

The Cilium manifest is a Go text template so one Bundle can be reused across
clusters. It must contain these placeholders in the Cilium API endpoint
settings:

```yaml
k8s-service-host: "{{ .APIServerHost }}"
k8s-service-port: "{{ .APIServerPort }}"
```

`PodCIDR` and `ClusterName` are also available as optional template values.

When `registry.enabled` is true, the Registry template must use
`{{ .RegistryPort }}` and `{{ .RegistryStoragePath }}`. It must render one
`kube-system/kubelift-registry` static Pod using `hostNetwork`, `hostPath`, the
label `app.kubernetes.io/name=kubelift-registry`, and `imagePullPolicy: Never`.

Create the compressed bundle and verify it immediately:

```bash
kubelift bundle create ./bundle-source \
  -o ./kubernetes-v1.28.15-amd64.tar.zst
```

Inspect a bundle independently:

```bash
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst --files
kubelift bundle inspect ./kubernetes-v1.28.15-amd64.tar.zst \
  --config /etc/kubelift/cluster.yaml
```

The second form also verifies the complete cluster installation profile,
including required binaries, systemd units, image archives, Cilium template,
Kubernetes version, and optional Registry payloads.

The bundle manifest records the Kubernetes version, supported Ubuntu versions, architecture, component versions, payload sizes, roles, and SHA-256 checksums. The archive uses `tar.zst` only as a transport format; it is not an official Kubernetes archive format.

Before any remote upload, KubeLift compares the target's `uname -m` and Ubuntu
`VERSION_ID` with the Bundle manifest. One Bundle targets one architecture
(`amd64` or `arm64`) and may list multiple supported Ubuntu versions. Master0 and
every additional node must match that manifest.

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

Prepare a remote node from the staged Bundle using the Sealos-style binary and
runtime layout:

```bash
kubelift bundle prepare 192.168.121.152
```

This installs the required `kubeadm`, `kubelet`, and `kubectl` binaries,
extracts the containerd runtime archive, installs the containerd and kubelet
systemd units, loads the required kernel modules, applies Kubernetes networking
sysctls, and enables/restarts containerd. It does not run `kubeadm`, import
images, or install Cilium yet.

Import the Kubernetes, Cilium, and optional Registry image archives into
containerd on a remote node:

```bash
kubelift bundle import-images 192.168.121.152
```

The command uses `ctr -n k8s.io images import --all-platforms` and never pulls
from a registry. Containerd must already be prepared and running on the target.

## Cluster Commands

Preview cluster creation without changing the host:

```bash
kubelift create --dry-run
```

Create the cluster on Master0, or resume a previously interrupted run:

```bash
kubelift create -f /etc/kubelift/cluster.yaml
kubelift create -f /etc/kubelift/cluster.yaml --resume
```

Add a worker or control-plane node over SSH, or preview either role:

```bash
kubelift add node 192.168.121.153 -f /etc/kubelift/cluster.yaml
kubelift add master 192.168.121.152 -f /etc/kubelift/cluster.yaml
kubelift add node 192.168.121.153 --dry-run
kubelift add master 192.168.121.152 --dry-run
```

`create` records phase state in `/var/lib/kubelift/state/<cluster>.yaml`. The state is bound to the cluster configuration and Bundle SHA-256 values. If a run is interrupted, KubeLift requires an explicit `--resume`; an ambiguous partial `kubeadm init` is never reset or rerun automatically.

`add node` and `add master` currently require the configured or overridden SSH
user to be `root`. Before changing the target, they check that it has not
already joined and that the Kubernetes ports required by its role are free:
`10250` for workers; `2379`, `2380`, `6443`, `10250`, `10257`, and `10259` for
control-plane nodes. They then upload and verify the Bundle, prepare containerd
and kubelet, directly import the Kubernetes and Cilium images, create
short-lived kubeadm credentials on Master0, upload a generated
`JoinConfiguration`, and wait for the node to become Ready. Registry images are
not copied to additional nodes. `add master` additionally uploads the
control-plane certificates with a short-lived certificate key and waits for
the new API server and etcd static Pods. A resumed operation skips the empty-port
check because the previous `kubeadm join` may already have started the services;
the persisted phase state controls the remaining work.

Node-addition progress is stored in
`/var/lib/kubelift/state/<cluster>-add-<role>-<address>.yaml`. Resume an
interrupted operation with the same arguments plus `--resume`. The state stores
no bootstrap token or certificate key. If KubeLift cannot determine whether
`kubeadm join` completed, it stops without rerunning join or invoking
`kubeadm reset`.

Adding a second control-plane node requires a stable `controlPlane.endpoint` in the configuration before cluster creation. A single control-plane cluster created without a stable endpoint cannot later be converted into a highly available control plane by `kubeadm` alone.

Two control-plane nodes are useful for testing the join path, but a two-member etcd cluster cannot tolerate either member failing. Use three control-plane nodes for actual control-plane fault tolerance.

Show only nodes, or include the critical system components installed by
KubeLift:

```bash
kubelift status
kubelift status --details -f /etc/kubelift/cluster.yaml
```

Detailed status is read-only and reports Nodes, Cilium, CoreDNS, and the
host-network Registry when it is enabled in the cluster configuration.

The v1.28 profile skips kube-proxy and uses Cilium as its full replacement.
For that profile KubeLift ignores only kubeadm's `FileExisting-conntrack`
preflight check; all other kubeadm preflight checks remain enforced.

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
├── status
└── version
```

Binary/runtime preparation, systemd setup, offline image import, and kubeadm
init configuration rendering are available. A guarded internal Master0
executor now stages the Bundle, prepares the host, imports images, and invokes
`kubeadm init`, renders/applies the Cilium manifest, and waits for the API
server, Cilium, nodes, and CoreDNS. It also starts and verifies the optional
host-network Registry static Pod cache. The public `create` command is enabled
with persisted phase state and explicit interrupted-run recovery. Worker and
additional control-plane joining are enabled through `add node` and `add master`.
