# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

**Core**
- Automatic image garbage collection, on by default: every cadre gets synthesized
  `rucher-prune.timer`/`rucher-prune.service` units running `podman image prune --all
  --filter until=…` as the cadre user, configured (or disabled) by the new manifest
  `prune:` block. The two unit names are reserved. Note: a binary downgraded below this
  version disables the timer but leaves the inert `.service` file behind (cosmetic).
- `ops validate` prints advisory `WARN` lines (exit code unchanged): a `PublishPort=`
  binding all interfaces (no host address, `0.0.0.0`, or `[::]`) is flagged with a hint
  to pin `127.0.0.1:<host>:<ctr>` unless the service is meant to be public.
- `ops init <name>` scaffolds a cadre directory: a commented manifest plus a minimal
  working `web.container` (loopback-published nginx) that passes `validate` cleanly.
- `ops validate` deep-checks Quadlet unit contents with Podman's own parser (pinned to
  podman v6): an unknown key, a missing `Image=`, an invalid value, or a dangling
  cross-reference now fails validation on the operator machine instead of only on the node.

### Changed

**Core**
- Loading a cadre now rejects a Quadlet file missing its type section (`[Container]` in a
  `.container`, …) — previously such a unit failed only on the node, at generation time.

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

[0.0.1]: https://github.com/igk1972/rucher/releases/tag/v0.0.1
