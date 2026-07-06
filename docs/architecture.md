# Architecture

The system has one core and three optional layers built on top of it.

```
              operator machine                          host in the fleet
       ┌───────────────────────────┐            ┌──────────────────────────────┐
       │ rucher ops ruches status/join│  SSH       │ sshd                          │
       │ (native Go SSH client,     │──────────► │                               │
       │  TOFU known_hosts)         │            │  rucher node agent run (timer)  │
       └───────────────────────────┘            │      │ pulls                   │
                                                 │      ▼                         │
   git / S3 store  ◄──────────────────────────── │  store checkout               │
   compartments/<name>/…                         │  placement.yml → assigned     │
   placement.yml                                 │      │                         │
                                                 │      ▼ reconcile.Apply         │
                                                 │  per-compartment Linux user    │
                                                 │  rucher-<name> (rootless podman) │
                                                 │  ~/.config/containers/systemd/ │
                                                 │  Quadlet units → systemd --user │
                                                 └──────────────────────────────┘
```

## 1. Compartments (the core model)

A **compartment** is an isolated workload environment backed by a dedicated Linux system
user (`rucher-<name>`) running rootless podman under its own per-user systemd manager. The
operator authors [Quadlet](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html)
unit files (`.container`, `.pod`, `.volume`, `.network`, `.kube`, `.image`, `.build`) plus
any support files; the tool lays them into the user's
`~/.config/containers/systemd/` directory, where podman's Quadlet generator turns them into
`.service` units. The tool never generates units — it treats them as opaque and reconciles
them. Native systemd then provides dependencies, lifecycle hooks and timers for free within
a compartment. See [compartments.md](compartments.md).

## 2. The `rucher` CLI and reconciler

`rucher` is a single static binary that runs as **root** on the host (it creates users,
manages linger and subuid/subgid ranges, and drives each user's systemd). The reconciler
(`internal/reconcile`, `internal/plan`) diffs a compartment's desired state against a
last-applied state file (hashes only, under `/var/lib/rucher/compartments/state/`)
and applies the minimal set of changes: write/remove files, create/remove podman secrets,
(re)apply resource limits, `daemon-reload`, and start/restart/stop only the affected units.
`apply` is idempotent; `plan` prints the same diff without touching the host. See
[cli.md](cli.md) and [compartments.md](compartments.md).

## 3. Secrets (SOPS + age)

Each compartment has its own age identity, generated in-process on the host. Its
`secrets.sops.yaml` is encrypted to that identity's recipient and is safe to commit to the
store. At `apply` time, root decrypts it (`sops -d`), keeps the plaintext in memory, and
feeds selected keys to podman as secrets over stdin — never to disk, never on argv. See
[secrets.md](secrets.md).

## 4. GitOps agent (pull-based reconcile)

Instead of pushing from an operator, each node can run `rucher node agent run` (typically on a
systemd timer via `rucher node agent install`). One pass: sync the store (git or S3) into a local
checkout, read `placement.yml`, compute the compartments assigned to this node, install each
one's unsealed identity, reconcile it, and unmanage compartments no longer assigned. A
status summary is written to `/var/lib/rucher/agent-status.json`. See
[gitops-agent.md](gitops-agent.md).

## 5. Management network (operator status plane)

`rucher ops ruches status` reaches every host over SSH (a native Go client with TOFU host-key
pinning — no system `ssh` binary required), reads each host's agent status file, and prints
an aggregated table or JSON. `rucher ops ruches join` records a host's reachability address into its
host config so the status plane can find it. See [management-network.md](management-network.md).

## 6. Compartment overlays (workload data plane)

A compartment can gain transparent cross-host L3 connectivity by including a
kernel-mode overlay sidecar in its pod. This needs no manager change: it is ordinary opaque
Quadlets plus the standard secrets mechanism. Privilege stays confined to the sidecar; the
app container shares the pod's network namespace unprivileged. This data plane is unrelated
to the operator management plane in (5). See [overlays.md](overlays.md).

## Data flow: one reconcile

1. **Desired state** is a compartment directory: `compartment.yml`, Quadlet units, support
   files, and (optionally) an encrypted `secrets.sops.yaml`. It comes either from a local
   `--dir` (imperative `rucher node apply`) or from the store checkout (GitOps agent).
2. **Load & validate** — parse the manifest (strict decode, unknown keys rejected), hash
   every support/unit file, validate units.
3. **Provision** — ensure the `rucher-<name>` user exists, has linger, a non-overlapping
   subuid/subgid block, and a live user systemd manager.
4. **Decrypt secrets** (if any) — root runs `sops -d` using the compartment's age identity;
   plaintext stays in memory.
5. **Plan** — diff desired file/secret/resource hashes against the last-applied state.
6. **Apply** — apply resource limits, stop removed units, write/remove files, create/remove
   secrets, perform registry logins, `daemon-reload`, then start/restart units.
7. **Persist** — write the new last-applied state (hashes only).

The same `reconcile.Apply` path is used by the imperative CLI and by the GitOps agent; the
only difference is where the compartment directory and the compartment's identity come from.
