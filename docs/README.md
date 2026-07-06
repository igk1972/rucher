# rucher

`rucher` (the built binary is `rucher`) is a config-driven
manager that runs each workload group as an isolated **compartment**. A compartment is a
dedicated rootless-podman environment owned by its own Linux user, materialized from
[Quadlet](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html) unit files
under per-user systemd, with its secrets encrypted at rest (SOPS + age). The tool does not
generate units — you author them — it reconciles a directory of compartments into running
services, idempotently. On top of the single-node reconciler it offers an optional
**pull-based GitOps agent** (each node reconciles the compartments a placement file assigns
to it, out of a git or S3 store), an **operator status/management plane** that queries every
node over SSH, and optional **per-compartment overlay networking** for transparent L3
connectivity between workloads across nodes.

## Table of contents

| Document | What it covers |
|----------|----------------|
| [architecture.md](architecture.md) | Components and how they fit; the reconcile data flow |
| [cli.md](cli.md) | Reference for every `rucher` command, its flags and an example |
| [compartments.md](compartments.md) | Compartment directory layout, manifest schema, the per-user rootless model, and how `plan`/`apply` reconcile |
| [secrets.md](secrets.md) | The SOPS/age secret model: per-compartment identity, encryption, podman secrets, decryption at apply |
| [gitops-agent.md](gitops-agent.md) | Pull-based reconcile: git/S3 store backends, `placement.yml`, node and sealed compartment identities, `node agent run`/`install` |
| [management-network.md](management-network.md) | `ops ruches join`, `ops ruches status`, the native Go SSH client with TOFU host-key pinning, address resolution |
| [overlays.md](overlays.md) | Per-compartment overlay networking for cross-node L3 between workloads |
| [node-requirements.md](node-requirements.md) | What each node in the fleet must provide |

## Validation runbooks

Manual end-to-end procedures run on Lima nodes (not part of `go test`); they also record what
was verified on real hardware. See [`validation/`](validation/):

| Runbook | Validates |
|---------|-----------|
| [integration-a.md](validation/integration-a.md) | Single-node core: new → apply → drift → idempotency → rm |
| [integration-b.md](validation/integration-b.md) | GitOps agent: node key → seal → git store → `node agent run` → removal |
| [integration-c.md](validation/integration-c.md) | Operator plane: `ops ruches status`, `--live`, `ops ruches join` |
| [integration-overlay.md](validation/integration-overlay.md) | Compartment overlay: kernel-mode sidecar, cross-node L3 |

## Terminology

- **Compartment** — one workload group: a directory ([compartments.md](compartments.md))
  reconciled into a dedicated Linux user's rootless-podman environment.
- **Node** — a Linux machine in the fleet that runs compartments. Reached over SSH
  by the operator plane; a node runs the GitOps agent to reconcile itself.
- **Operator** — the machine an engineer drives `rucher` from to query and manage the fleet.
- **Store** — the git or S3 source of truth the GitOps agent pulls
  ([gitops-agent.md](gitops-agent.md)).
