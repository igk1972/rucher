# rucher

`rucher` (the built binary is `rucher`) is a config-driven
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

## Validation runbooks

Manual end-to-end procedures run on Lima nodes (not part of `go test`); they also record what
was verified on real hardware. See [`validation/`](validation/):

| Runbook | Validates |
|---------|-----------|
| [integration-a.md](validation/integration-a.md) | Single-node core: new → apply → drift → idempotency → rm |
| [integration-b.md](validation/integration-b.md) | GitOps agent: node key → seal → git store → `node agent run` → removal |
| [integration-c.md](validation/integration-c.md) | Operator plane: `ops nodes status`, `--live`, `ops nodes join` |
| [integration-overlay.md](validation/integration-overlay.md) | Cadre overlay: kernel-mode sidecar, cross-node L3 |

## Terminology

- **Cadre** — one workload group: a directory ([cadres.md](cadres.md))
  reconciled into a dedicated Linux user's rootless-podman environment.
- **Node** — a Linux machine that runs cadres. Reached over SSH
  by the operator plane; a node runs the GitOps agent to reconcile itself.
- **Operator** — the machine an engineer drives `rucher` from to query and manage the nodes.
- **Store** — the git or S3 source of truth the GitOps agent pulls
  ([gitops-agent.md](gitops-agent.md)).
