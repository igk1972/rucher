# Compartment overlays

A compartment can give its workloads **transparent L3 connectivity across hosts** — a private
mesh where an app on one host reaches an app on another by a stable overlay IP, with no proxy
and no application changes. This is a per-workload data plane, distinct from the operator
[management network](management-network.md) (`net join`, which sets a *host's* management
address).

## How it fits the model — no manager change

An overlay is just ordinary opaque Quadlets plus the standard secrets mechanism; the manager
lays them down as-is. The compartment's pod contains two containers:

- an **overlay sidecar** running a mesh VPN in **kernel mode**, which brings up a real network
  interface (e.g. `tailscale0`) inside the pod and joins the mesh, receiving an overlay
  address;
- the **application container**, in the same pod, sharing the pod's network namespace.

Because they share the netns, the app transparently uses the sidecar's overlay interface —
the kernel routes traffic through it. Privilege stays confined to the sidecar.

## Kernel mode is required

The overlay must run in **kernel mode**, not userspace mode. A typical mesh-VPN image defaults
to userspace mode (a SOCKS5/HTTP proxy), which is not transparent — the app would have to
opt in to the proxy. Kernel mode requires, on the sidecar container only:

- `AddDevice=/dev/net/tun` — access to the TUN device;
- `AddCapability=NET_ADMIN` and `AddCapability=NET_RAW`;
- `TS_USERSPACE=false` (for the tailscale image) so it creates a real interface instead of a
  proxy.

With these, the sidecar brings up the overlay interface and the kernel routes packets over it
transparently.

## Privilege confined to the sidecar

Only the sidecar holds `/dev/net/tun` and the `NET_ADMIN`/`NET_RAW` capabilities. The
application container carries **no** device and **no** added capability — it is an ordinary
unprivileged container that happens to share the pod's netns and therefore the overlay
interface. Overlay membership is per compartment: each compartment that wants mesh
connectivity ships its own sidecar and gets its own overlay address.

## The auth key rides `secrets.create`

The sidecar authenticates to the mesh with an auth key, delivered through the normal secrets
path (see [secrets.md](secrets.md)):

```yaml
# compartment.yml
name: overlay-demo
secrets:
  from: secrets.sops.yaml
  create:
    - ts-authkey            # only this key becomes a podman secret
```

The encrypted `secrets.sops.yaml` holds `ts-authkey`; at apply the manager materializes it as
a podman secret, and the sidecar consumes it as an environment variable:

```ini
# overlay-ts.container (sidecar, kernel mode)
[Container]
Pod=overlay-demo.pod
AddDevice=/dev/net/tun
AddCapability=NET_ADMIN
AddCapability=NET_RAW
Secret=ts-authkey,type=env,target=TS_AUTHKEY
Environment=TS_USERSPACE=false ...
```

```ini
# overlay-app.container (workload) — no device, no capability
[Container]
Pod=overlay-demo.pod
Image=docker.io/library/nginx:alpine
```

Never commit a real auth key in plaintext — commit only the encrypted `secrets.sops.yaml`.

## Host prerequisite

Kernel mode needs the `tun` kernel module loaded and `/dev/net/tun` accessible to the
compartment's user. The manager does not set this up; it belongs to the provisioning layer
(see [host-requirements.md](host-requirements.md)). If the device is missing or its
permissions are insufficient, a kernel-mode sidecar cannot bring up its interface.

## Worked example

A complete, ready-to-apply overlay compartment lives at
[`../test/overlay-example/`](../test/overlay-example/): the manifest, the pod, the kernel-mode
sidecar unit, the unprivileged app unit, and a plaintext secrets template. Apply it like any
compartment — remember `--dir` is the **parent** directory and the name selects the
subdirectory (run from the module root):

```bash
sudo rucher apply --dir ./test/overlay-example overlay-demo
```
