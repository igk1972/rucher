# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-16

### Added

**Core**
- Automatic image garbage collection, on by default: every cadre gets synthesized
  `rucher-prune.timer`/`rucher-prune.service` units running `podman image prune --all
  --filter until=…` as the cadre user, configured (or disabled) by the new manifest
  `prune:` block. The two unit names are reserved. Note: a binary downgraded below this
  version disables the timer but leaves the inert `.service` file behind (cosmetic).
- Cadres can ship their own native `.service` units. They are installed into the user unit
  dir alongside `.timer`/`.socket`/`.path`; one carrying an `[Install]` section is enabled
  (`enable --now`, restarted on change, disabled on removal), while an `[Install]`-less
  oneshot is only installed and daemon-reloaded, left for a companion unit to fire. A cadre
  `.service` may not shadow the `.service` Quadlet generates from one of the cadre's units.
- `ops validate` prints advisory `WARN` lines (exit code unchanged): a `PublishPort=`
  binding all interfaces (no host address, `0.0.0.0`, or `[::]`) is flagged with a hint
  to pin `127.0.0.1:<host>:<ctr>` unless the service is meant to be public.
- `ops init <name>` scaffolds a cadre directory: a commented manifest plus a minimal
  working `web.container` (loopback-published nginx) that passes `validate` cleanly.
- `ops validate` deep-checks Quadlet unit contents with Podman's own parser (pinned to
  podman v6): an unknown key, a missing `Image=`, an invalid value, or a dangling
  cross-reference now fails validation on the operator machine instead of only on the node.
- `-h`, `--help`, and `help` are recognized as help requests at every command group: each
  prints that group's usage and exits `0` (scoped, e.g. `rucher node --help`,
  `rucher ops nodes --help`). A genuinely unknown command still prints `unknown command: …`
  and exits non-zero.

**Multi-node**
- `ops nodes deploy` accepts `--store-user` to set the git store user when it isn't embedded
  in the store URL.

### Changed

**Core**
- Loading a cadre now rejects a Quadlet file missing its type section (`[Container]` in a
  `.container`, …) — previously such a unit failed only on the node, at generation time.
- The S3 store now uses **TLS by default** (`useSSL` defaults to `true`); a plaintext HTTP
  endpoint must be opted into explicitly with `useSSL: false` in the agent config.
- Node config strictness now lives in `ops validate`: the runtime path (`deploy`/`status`)
  tolerates an unknown field a co-located tool may own (e.g. a shared `podman.registries`
  block), while `ops validate` strict-checks each node's `configuration.yml` and fails on a typo.
- The manifest validates `resources.memoryMax` / `resources.cpuQuota`, rejecting a malformed
  value (or one containing a newline) at load time.
- `plan` output now lists enable/disable/stop/remove/secret actions, not only file writes, so a
  dry run shows the full set of changes a reconcile will make.
- `node cadre rm --purge` stops the cadre's workloads before removing them, a graceful teardown
  instead of an abrupt unit deletion.
- The CLI rejects an unrecognized flag or an extra argument across subcommands (e.g. a typo'd
  `--llive`, or a bare `--config`) instead of silently treating it as a node/cadre name or
  falling back to a default.

### Fixed

**Core**
- `quadletref` now extracts references the way podman's parser does — joining `\` line
  continuations and handling the `--flag=value`, quoted, and `src=` forms — so a changed
  support file or rotated secret restarts the units that actually depend on it (a missed
  secret reference previously left a container running with a stale credential).
- A cadre uid change re-applies its files, podman secrets, and units (not only resource
  limits), so restoring state onto a rebuilt node no longer leaves the workload
  under-provisioned.
- The agent records a pass-level failure (store sync, placement) in its status, so a node
  whose reconcile pass failed no longer reads as healthy in `ops nodes status`.
- Provisioning and identity writes fail on a non-zero command exit code instead of silently
  continuing — most importantly resource-limit drop-ins, which could otherwise be dropped
  and never retried.
- A unit whose stop fails is retained for the next reconcile instead of being deleted and
  forgotten while still running.
- The coarse reload fallback also restarts native systemd units (`.timer`/`.socket`/`.path`)
  that depend on a changed support file.
- Long unit-file lines no longer silently truncate parsing/validation.
- The `deploy` command rejects an unknown flag instead of swallowing it (and its value) as a
  node name; the reconcile pass is bounded by a timeout so a stalled store can't pin the node
  lock; the registry login re-runs when only the login block changes; `state.json` is fsynced
  before its atomic rename; and `reconcile.Remove` validates the cadre name.
- Cadre identity keys are written via `cat` under `umask 077` rather than `install /dev/stdin`,
  which a non-root cadre user cannot re-open through the manager's pipe — provisioning failed
  on some systems.
- `ops nodes status` reads the 0600 `agent-status.json` over `sudo`, so status works when the
  operator's SSH user is not root.
- `ops nodes status` no longer reports a reachable node whose agent has not yet written
  `agent-status.json` as unreachable. A freshly deployed node, or a push-mode fleet driven by
  `node cadre apply` with no pull agent, now shows `REACHABLE=yes` with a `pending` revision
  (JSON `"pending": true`) and does not count toward the command's non-zero exit code; `--live`
  still probes such nodes. A genuine transport/SSH failure, a reconcile error, or a status-file
  read that fails for another reason (e.g. sudo is not passwordless) still marks the node
  unreachable or bumps the exit code.

### Security

**Core**
- Bound the output captured from a remote command, so a malicious or misbehaving node can no
  longer exhaust operator memory by streaming unbounded output during a fleet sweep.
- Harden the SOPS+age decoder: a tampered file with a wrong-length IV no longer panics, and
  empty/duplicate-key malleability that could blank a secret while the MAC still verified is
  rejected.
- Guard podman positional arguments (registry, secret name) with `--` so a value beginning
  with `-` cannot be reinterpreted as a flag.
- Validate cadre names in the operator-side `keygen` / `net join` / `secrets encrypt`
  commands, matching `ops init`.
- The `known_hosts` fallback used when no home directory is available is now a per-user 0700
  path instead of a predictable, world-writable `/tmp` file.
- Release binaries are built with Go 1.26.5, closing an Encrypted Client Hello privacy leak
  in `crypto/tls` (GO-2026-5856) reachable from the S3 store and age decryption paths.

## [0.0.1] - 2026-07-10

First release. **rucher** is a multi-node manager for podman **Quadlet** workloads: each
workload group runs as an isolated *cadre* — a directory of Quadlet units reconciled into
rootless-podman services under per-user systemd, managed across many nodes from one operator.

### Added

**Core**
- Idempotent single-node reconciler: `apply` lays down units + support files, creates podman
  secrets, reloads systemd and starts services; reconciles drift so a changed support file
  restarts only the units that use it.
- Secrets encrypted at rest (SOPS + age) in the cadre directory; decrypted in memory and fed
  to podman over stdin — never written to disk or passed on argv.
- Per-cadre isolation: a dedicated user, its own podman secret and registry-credential store,
  its own age identity, and an optional systemd resource slice (`memoryMax`/`cpuQuota`).

**Multi-node**
- Pull-based GitOps agent: each node reconciles the cadres a `placement.yml` assigns it, from
  a git or S3 store.
- Operator plane over SSH: `ops nodes deploy`/`status` (parallel, `--concurrency`), plus
  `ops validate` and `ops plan` for pre-commit checks.
- Per-cadre overlay networking: a kernel-mode tailscale/headscale sidecar, validated on a
  real tailnet.

**Node provisioning**
- `ops nodes deploy` installs podman from the distro (apt), journald-capable, by default, or
  prebuilt podman 6.x `.deb` via `--podman-prebuilt` (source `igk1972/podman-6-deb`).
- Cadre users get regular uids and a per-user rootless `storage.conf`, so `podman logs` and
  `journalctl --user` work under the cadre user.

### Binaries

linux and darwin (amd64/arm64), windows (amd64), with `SHA256SUMS.txt`.

[0.1.0]: https://github.com/igk1972/rucher/releases/tag/v0.1.0
[0.0.1]: https://github.com/igk1972/rucher/releases/tag/v0.0.1
