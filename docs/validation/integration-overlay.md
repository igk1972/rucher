# Cadre overlay — real-tailnet validation record

A human-verified record of the cadre-overlay data plane on **real hardware against a real
Tailscale tailnet** — the one thing the automated suite does not reproduce: it relays cross-node
traffic through a self-hosted headscale + embedded DERP, not a real tailnet with direct routing.

- **Concepts** (kernel mode, `TS_USERSPACE=false`, privilege confined to the sidecar, per-cadre
  membership, the authkey via `secrets.create`): see [`../overlays.md`](../overlays.md).
- **Ready example**: [`../examples/overlay-example/`](../examples/overlay-example/).
- **Automated cross-node test** (over a self-hosted headscale, no SaaS auth key):
  `test/integration/headscale_test.go` (`TestHeadscaleOverlayCrossNode`).

## What was verified (controller, on a real tailnet)

On Lima (Debian trixie, podman 5.8.4) against a real Tailscale tailnet:

- The kernel-mode sidecar registered in the tailnet and brought up `tailscale0` with a `100.x`
  address; the unprivileged `overlay-app` in the same pod uses it with **no** device or capability.
- The app in the pod on lima-01 reached nginx in the pod on lima-02 by its tailscale IP —
  without app changes and without a proxy — and the kernel routed it **directly** `dev tailscale0`.
  This direct-routing evidence is what a real tailnet adds over the automated headscale/DERP test
  (whose isolated Lima usernets can only ever relay).
- Applied and torn down as an ordinary cadre via `rucher node cadre new` → `apply` → `rm`.

## Node-side checks (as the cadre user)

```bash
# the sidecar's tailnet address:
podman exec overlay-ts tailscale ip -4
# the outbound route goes directly through tailscale0 (kernel mode, not a userspace proxy or relay):
podman exec overlay-app ip route get <tailscale-IP-on-another-node>
# end-to-end from the workload, unchanged:
podman exec overlay-app wget -qO- http://<tailscale-IP-of-nginx-on-lima-02>/
```

## SaaS authkey lifecycle

The authkey comes from the Tailscale admin console (Settings → Keys). With an ephemeral key the
node leaves the tailnet as soon as the sidecar stops; otherwise delete it manually in the admin
console. `TS_STATE_DIR=/tmp/tsstate` keeps the sidecar's state inside the container — there is no
separate volume for it here.
