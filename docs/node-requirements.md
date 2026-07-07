# Node requirements

Each node runs the `rucher` binary as **root** and runs cadres as rootless
podman under per-user systemd. The requirements below are prerequisites that the provisioning
tooling ensures on every node; the manager assumes they are present.

## Base platform

- A Linux node with **systemd**, including per-user managers: `loginctl` (for
  `enable-linger`), `runuser`, the `user@<uid>.service` template, and `journalctl` (used by
  `rucher node cadre logs`; `node cadre status` uses `systemctl --user show`).
- The `rucher` binary installed and runnable as root. For the GitOps timer, install it at
  `/usr/local/bin/rucher` — the unit written by `rucher node agent install` invokes that exact path.

## podman (rootless)

- **podman**, rootless-capable. The provisioning tooling installs a statically linked podman
  build with all dependencies bundled, so the node needs no distro podman packaging.
- **Rootless prerequisites**:
  - the `uidmap` package providing the setuid helpers `newuidmap` / `newgidmap`;
  - `/etc/subuid` and `/etc/subgid` present. The manager allocates a unique,
    non-overlapping 65536-ID subuid/subgid block per cadre user
    (`usermod --add-subuids/--add-subgids`), so these files must exist and be writable by
    root; existing ranges are respected.

Each cadre gets a dedicated `rucher-<name>` system user with linger enabled, its own
podman secret store and a running user systemd manager (see [cadres.md](cadres.md)).

## Secret decryption

- The **`sops` binary** on `PATH`. `apply` decrypts each cadre's `secrets.sops.yaml`
  with `sops -d`, using the cadre's age identity via `SOPS_AGE_KEY_FILE`.
- **No separate age tooling is needed.** age identities are generated in-process by the
  manager, and decryption uses SOPS's built-in age backend — there is no age CLI dependency
  on the node. See [secrets.md](secrets.md).

## GitOps store access (if the agent is used)

- The git store uses an **in-process** git client, so **no system `git` binary** is required
  on the worker. For a git-over-SSH store, `~/.ssh/known_hosts` must be seeded for the remote
  (or the agent config sets `insecureHostKey: true`). The S3 store needs only network access
  to the endpoint. See [gitops-agent.md](gitops-agent.md).
- The node's own age key is created by `rucher node key init` at
  `/etc/rucher/node/identity.txt`.

## SSH reachability (operator plane)

- Nodes are reached from the operator over SSH by the manager's built-in Go SSH client, so a
  node only needs a standard **sshd** and a reachable address (recorded via `rucher ops nodes join`).
  No `ssh` binary is required on the operator machine. See
  [management-network.md](management-network.md).

## Cadre overlays (only if used)

- The **`tun` kernel module** loaded and **`/dev/net/tun`** present and accessible to the
  cadre's user. Only needed for cadres that run a kernel-mode overlay sidecar;
  the manager does not configure it. See [overlays.md](overlays.md).

## Summary

| Requirement | Needed for | Notes |
|-------------|-----------|-------|
| systemd + per-user managers | always | linger, `runuser`, `user@.service`, `journalctl` |
| podman (static build) | always | rootless |
| `uidmap`, `/etc/subuid`+`/etc/subgid` | always | per-cadre subuid/subgid ranges |
| `sops` binary | secrets | age backend is built into sops |
| standard `sshd` | operator plane | native Go SSH client from the operator |
| `tun` module + `/dev/net/tun` | overlays only | kernel-mode sidecar |
