# Example: overlay cadre (L3 mesh via tailscale sidecar)

A ready-made cadre that gives its workloads transparent L3 connectivity on the tailnet
between nodes — without changing the manager's code. These are ordinary "opaque" quadlets: the
manager lays them down as-is, and the authkey travels through the standard secrets mechanism
(`secrets.create` -> podman secret -> sidecar env).

## What's inside

| File | Role |
|------|------|
| `rucher.yml` | manifest; `secrets.create: [ts-authkey]` turns the authkey into a podman secret |
| `overlay-demo.pod` | pod, shared netns for the sidecar and the workload |
| `overlay-ts.container` | tailscale sidecar in **kernel mode** (`/dev/net/tun`, `NET_ADMIN`/`NET_RAW`, `TS_USERSPACE=false`) — brings up `tailscale0` with a `100.x` address |
| `overlay-app.container` | the actual workload (nginx); **no** device or capability — transparently reaches the tailnet through the pod's netns |
| `secrets.sops.example.yaml` | PLAINTEXT template for the authkey; encrypt it into `secrets.sops.yaml`, never commit the real key |

The privilege is locked inside the sidecar: only `overlay-ts` holds `/dev/net/tun` and the
capabilities, while `overlay-app` is an ordinary unprivileged container that still uses the same
`tailscale0`, because it shares the pod's netns.

## How to apply

The cadre's files live in the `overlay-demo/` subdirectory. `apply` takes the **parent**
directory (`--dir`) and the name selects the subdirectory — that is, `--dir` points at this
directory (`docs/examples/overlay-example/`), not at `overlay-demo/` itself. The commands below
are run from this directory:

```bash
# 1. authkey from the tailscale admin console -> encrypt it for this cadre's recipient
#    (into overlay-demo/secrets.sops.yaml, next to rucher.yml):
rucher node cadre recipient overlay-demo              # -> age1...
printf 'ts-authkey: tskey-auth-XXXX\n' \
  | sops --encrypt --input-type yaml --output-type yaml --age <recipient> /dev/stdin \
  > overlay-demo/secrets.sops.yaml

# 2. lay down and start (--dir = parent, overlay-demo = subdirectory; or via a GitOps agent):
rucher node cadre apply --dir . overlay-demo
```

The node must have the `tun` module loaded and `/dev/net/tun` accessible to the cadre's
user — that's the provisioning layer's job (see `docs/node-requirements.md`).

For the concepts and walkthrough see `docs/overlays.md`. The automated cross-node test (over a
self-hosted headscale, no SaaS auth key) is `test/integration/headscale_test.go`; the real-tailnet
validation record is `docs/validation/integration-overlay.md`.
