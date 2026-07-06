# Compartment overlay — run on Lima nodes

Gives a compartment's workloads transparent L3 connectivity in the tailnet between hosts. The form is
ordinary "opaque" quadlets: the operator writes a tailscale sidecar + pod as usual, and the authkey
travels via the regular `secrets.create`. **The manager's code does not change.** The full example is
`test/overlay-example/` (the compartment itself lives in the subdirectory `overlay-example/overlay-demo/`).

**Validated by the controller** on Lima (Debian trixie, podman 5.8.4, a real tailnet):
cross-node transparent connectivity from a pod on lima-01 to nginx in a pod on lima-02 over the tailscale IP,
without workload changes and without a proxy; the kernel routes `dev tailscale0`. Below it is marked what
the controller verified and what remains an operator step.

## How it differs from control network C

- **Control network C** (`pecm net join <host> --address 100.64.0.1`) is the control plane:
  the host's own address, over which the operator/manager reaches the node. It is written to
  `./hosts/<host>/configuration.yml` as `network: {address}`. Level — host.
- **Compartment overlay** (this run) is the data plane: tailnet membership of a specific
  workload. A sidecar inside the compartment's pod gives that compartment its own `100.x` address.
  Level — the workload, tied to a single compartment. The two are unrelated:
  the overlay works even if the hosts see each other with no C network at all.

## Host prerequisite (a provisioning step, not the manager's)

- The `tun` kernel module is loaded and `/dev/net/tun` is accessible to the compartment's user
  (on the nodes it was `0666`). Check: `test -c /dev/net/tun && stat -c %a /dev/net/tun`.
- The manager does NOT do this — it belongs in the provisioning layer (`podman-essaim` / node image).
  If the device is missing or permissions are insufficient, a sidecar with `TS_USERSPACE=false` won't bring up
  `tailscale0`.

## Why `TS_USERSPACE=false` (critical)

The `docker.io/tailscale/tailscale` image **defaults to userspace mode** (SOCKS5/HTTP
proxy) — this is NOT transparent: the workload would have to explicitly go through the proxy. We need
kernel mode: `TS_USERSPACE=false` + `/dev/net/tun` + `NET_ADMIN`/`NET_RAW`. Then the sidecar
creates a real `tailscale0` interface, and the kernel routes traffic `dev tailscale0`
transparently — the application has no idea it goes through the tailnet.

## Membership per compartment, privilege in the sidecar

- Tailnet membership is at the compartment level: each has its own sidecar and its own `100.x`.
- Privilege is locked in the sidecar. `/dev/net/tun`, `NET_ADMIN`, `NET_RAW` are held only by
  `overlay-ts`. `overlay-app` is an ordinary unprivileged container (no device/cap),
  but uses the same `tailscale0` because it shares the `overlay-demo` pod's netns.

## Authkey via `secrets.create`

- Get the key from the tailscale admin console (Settings -> Keys -> Auth keys; reusable + pre-approved is convenient).
- Encrypt it to THIS compartment's age recipient in `secrets.sops.yaml`:

  ```bash
  pecm age recipient overlay-demo                     # -> age1... the compartment's recipient
  printf 'ts-authkey: tskey-auth-XXXX\n' \
    | sops --encrypt --input-type yaml --output-type yaml --age <recipient> /dev/stdin \
    > test/overlay-example/overlay-demo/secrets.sops.yaml
  ```

  (`--input-type yaml` is mandatory — otherwise sops wraps everything in a single `data` key; see run B.
  `secrets.sops.yaml` goes into the compartment's subdirectory, next to `compartment.yml`.)
- In `compartment.yml`: `secrets.create: [ts-authkey]` — only this key becomes a podman
  secret. The sidecar picks it up via `Secret=ts-authkey,type=env,target=TS_AUTHKEY`
  (podman secret -> env `TS_AUTHKEY`). Do NOT commit the real key in plaintext —
  `secrets.sops.example.yaml` is only a format sample.

## Applying via the manager (an operator step)

Lay it out and run it as an ordinary compartment — no manager changes required. `--dir` is
the **parent** directory (the one that contains the `overlay-demo/` compartment subdirectory),
and the name selects the subdirectory; verified by the controller via a full `pecm new` → `apply` → `rm`:

```bash
# local/direct apply on the node (--dir = parent, overlay-demo = subdirectory):
sudo pecm apply --dir ./test/overlay-example overlay-demo

# or via the GitOps agent (run B): commit the compartment into the store,
# placement.yml -> overlay-demo: <node>, then `sudo pecm agent run`.
```

The quadlet form the manager applies was verified by the controller via `systemctl --user`:
pod + sidecar + app unit come up, the sidecar gets a tailnet address, the authkey is delivered via
podman secret -> env.

## What exactly was verified (controller)

- The sidecar in kernel mode registered in the tailnet and brought up `tailscale0` with IP `100.x`.
- The unprivileged `overlay-app` in the same pod transparently uses `tailscale0` (no
  device, no cap).
- The app in the pod on lima-01 reached nginx in the pod on lima-02 by its tailscale IP — without app changes
  and without a proxy; the kernel routes `dev tailscale0`.

Quick checks on the node:

```bash
# the sidecar's tailnet address:
podman exec overlay-ts tailscale ip -4
# the outbound route goes through tailscale0 (kernel mode, not a userspace proxy):
podman exec overlay-app ip route get <tailscale-IP-on-another-node>
# end-to-end connectivity from the workload without app changes:
podman exec overlay-app wget -qO- http://<tailscale-IP-of-nginx-on-lima-02>/
```

## Cleanup

```bash
sudo pecm rm overlay-demo --purge     # stop units, unmanage, remove user+data
```

The node leaves the tailnet on its own once the sidecar is stopped (for an ephemeral authkey — immediately;
otherwise delete it manually in the tailscale admin console). `TS_STATE_DIR=/tmp/tsstate` in the sidecar lives inside
the container, there is no separate volume for state here.
