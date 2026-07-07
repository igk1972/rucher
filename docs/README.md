# rucher

`rucher` (the built binary is `rucher`) is a config-driven, multi-node
manager that runs each workload group as an isolated **cadre**. A cadre is a
dedicated rootless-podman environment owned by its own Linux user, materialized from
[Quadlet](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html) unit files
under per-user systemd, with its secrets encrypted at rest (SOPS + age). The tool does not
generate units — you author them — it reconciles a directory of cadres into running
services, idempotently. On top of the single-node reconciler it offers an optional
**pull-based GitOps agent** (each node reconciles the cadres a placement file assigns
to it, out of a git or S3 store), an **operator status/management plane** that queries every
node over SSH, and optional **per-cadre overlay networking** for transparent L3
connectivity between workloads across nodes.

## Table of contents

| Document | What it covers |
|----------|----------------|
| [architecture.md](architecture.md) | Components and how they fit; the reconcile data flow |
| [cli.md](cli.md) | Reference for every `rucher` command, its flags and an example |
| [cadres.md](cadres.md) | Cadre directory layout, manifest schema, the per-user rootless model, and how `plan`/`apply` reconcile |
| [secrets.md](secrets.md) | The SOPS/age secret model: per-cadre identity, encryption, podman secrets, decryption at apply |
| [gitops-agent.md](gitops-agent.md) | Pull-based reconcile: git/S3 store backends, `placement.yml`, node and sealed cadre identities, `node agent run`/`install` |
| [management-network.md](management-network.md) | `ops nodes join`, `ops nodes status`, the native Go SSH client with TOFU host-key pinning, address resolution |
| [overlays.md](overlays.md) | Per-cadre overlay networking for cross-node L3 between workloads |
| [node-requirements.md](node-requirements.md) | What each node must provide |

## End-to-end tests and validation

Automated end-to-end tests (Go, build tag `integration`) drive real Lima nodes and cover the
single-node core, the GitOps agent (git + S3), the operator plane, cadre isolation, and a
headscale overlay. See [`../test/integration/`](../test/integration/) and its README.

One manual record remains for what only real hardware can show — the overlay on a real
**Tailscale tailnet** with **direct kernel routing**, which the automated test relays through a
self-hosted headscale/DERP instead:

| Record | Captures |
|--------|----------|
| [integration-overlay.md](validation/integration-overlay.md) | Cadre overlay verified on a real tailnet (kernel routing `dev tailscale0`) |

## Terminology

- **Cadre** — one workload group: a directory ([cadres.md](cadres.md))
  reconciled into a dedicated Linux user's rootless-podman environment.
- **Node** — a Linux machine that runs cadres. Reached over SSH
  by the operator plane; a node runs the GitOps agent to reconcile itself.
- **Operator** — the machine an engineer drives `rucher` from to query and manage the nodes.
- **Store** — the git or S3 source of truth the GitOps agent pulls
  ([gitops-agent.md](gitops-agent.md)).
