# Integration tests (Lima)

End-to-end tests that drive the real Lima nodes. They are gated behind the
`integration` build tag, so `go test ./...` never touches a node.

```bash
go test -tags integration ./test/integration/ -v
```

## Prerequisites

The tests **do not provision anything** — they **fail** when a node they need is not
`Running` (and likewise when a required host tool is missing); they never skip. Bringing
the nodes up and installing their toolchain is a separate step (see Setup below).

- Nodes `lima-essaim-01/02/03` `Running` (`limactl list`), each with `podman`, `uidmap`,
  and `/dev/net/tun`.
- Host tooling: `go`, `limactl`, `git`, `sops` (host-only: builds encrypted test fixtures
  and backs the sops-cross-compat check; not a runtime dependency), `openssl` + `gh`
  (headscale overlay test), `rclone` (S3 store test — that test skips if absent).

The suite builds `rucher` **for each node's architecture** (arm64 or amd64, probed via
`dpkg --print-architecture`), installs it at `/usr/local/bin/rucher`, and drives the
nodes over `limactl shell`. Operator-side commands (`ops nodes status …`) run from a
host-built binary, exercising the project's own `sshx` client against the Lima
`ssh.config`.

## Setup (local and CI)

`cmd/setup-nodes` (a small Go program) creates the Lima swarm and installs podman
(static bundle) + uidmap + `/dev/net/tun` on each node — the same self-contained
recipe on a Mac and in CI (no external tooling; distilled from the `lima-essaim` /
`podman-essaim` skills):

```bash
go run ./test/integration/cmd/setup-nodes            # create + provision + verify
go run ./test/integration/cmd/setup-nodes verify     # just show per-node state
```

It is idempotent (skips a node already at the target podman version) and never
clobbers existing `../nodes/<name>/configuration.yml`. Tunables: `RUCHER_IT_COUNT`,
`RUCHER_IT_PODMAN`, `RUCHER_IT_TEMPLATE`, `RUCHER_IT_CPUS/MEMORY/DISK`.

CI: `.github/workflows/integration.yml` runs the nodes in real kernel VMs via Lima on an
`ubuntu-latest` runner (`lima-vm/lima-actions/setup`, KVM enabled by a udev rule, VM
images cached), then runs setup + `go test -tags integration`.

## What runs where

- **The store** is served over smart HTTP by an in-process Go server
  (`git-http-backend` as CGI) bound on the host; the guests clone it over the Lima
  gateway (`host.lima.internal`). This mirrors a real deployment pulling from
  `https://…` and keeps the **nodes free of any git binary** (go-git is pure Go over
  http). git runs only on the host.
- Each store lives under `$HOME/.cache/rucher-integration/` (removed per test).
- **Direct-apply tests** stage the cadre onto the node with `limactl copy` and apply from
  a node-local dir — files reach the node the way they do in a real deployment, so the
  suite never reads cadre files over the Lima mount (any VM backend / `--plain` works).

## Tests

**Regressions** (`core_test.go`)
| Test | Covers |
|------|--------|
| `TestLiveShowsUnitStatus`      | `ops nodes status --live` runs `node cadre status`, not the removed `rucher status` |
| `TestExtraSopsNotMaterialized` | every `*.sops.yaml` is a service file, not just `.sops.yaml` |

**Single-node core** (`singlenode_test.go`)
| Test | Covers |
|------|--------|
| `TestNewProvisionsUserAndIdentity` | `node cadre new` creates the user + age identity (0600), idempotent |
| `TestIdempotentApply`              | a no-change apply starts/restarts nothing |
| `TestRemoveKeepsUserPurgeDeletes`  | `rm` keeps the OS user, `rm --purge` deletes it |

**Live container** (`container_test.go`)
| Test | Covers |
|------|--------|
| `TestSecretReachesContainerEnv`             | SOPS → podman secret → env var inside the running container |
| `TestSelectiveRestartOnSupportFileChange`   | editing a support file restarts only the units that reference it |

**Multi-node GitOps** (`multinode_test.go`)
| Test | Covers |
|------|--------|
| `TestPlacementAcrossNodes`   | one placement fans a cadre out to several nodes; an unassigned node leaves it alone |
| `TestCadreMigration`         | changing a placement migrates a cadre (old node unmanages, keeps the user; new node applies) |
| `TestSealedIdentityNegative` | a cadre sealed only to node-01 cannot be unsealed/applied on node-02 |
| `TestS3StorePlacement` (`s3_test.go`) | the agent reconciles from an S3 store (`rclone serve s3`), not just git |

**Isolation** (`isolation_test.go`)
| Test | Covers |
|------|--------|
| `TestSubuidBlocksDisjoint`      | each cadre user gets a unique, non-overlapping 65536-id subuid block |
| `TestCrossCadreSecretIsolation` | a cadre cannot see another cadre's podman secrets |
| `TestResourceLimitsApplied`     | manifest `resources` become a systemd slice drop-in |

**Negative / resilience** (`negative_test.go`)
| Test | Covers |
|------|--------|
| `TestUnknownManifestKeyRejected`   | an unknown key in `rucher.yml` fails the apply (strict decode) |
| `TestPlacementTypoDoesNotUnmanage` | a `placement:` typo errors instead of unmanaging every cadre |
| `TestStoreLastGoodOnFetchFailure`  | a fetch failure on a valid checkout keeps running on the last-good revision |

**Operator plane / security** (`operator_test.go`, `security_test.go`)
| Test | Covers |
|------|--------|
| `TestNodeUnreachable`                  | a down node shows REACHABLE=no and exits non-zero (stops/restarts a node) |
| `TestSensitiveFilePermissions`         | the age identity and state file are mode 0600 |
| `TestSecretPlaintextNotInStateOrUnits` | secret plaintext never lands in the state file or the systemd dir |

Deep-merge of node configs and host-key TOFU rotation are covered by unit tests in
`internal/nodecfg` and `internal/sshx` (`TestAcceptNewTOFU`), so they have no e2e here.

## Caveats

- The agent caches its store checkout at a fixed path and pulls from the cached
  repo's origin, so each test resets `/var/lib/rucher/store` before running.
- If a Lima VM is rebuilt, its SSH host key changes; clear its line from
  `~/.config/rucher/known_hosts` before re-running the status tests.
- Tests purge their own cadres (`node cadre rm --purge`) on cleanup. The node's own
  age key (`node key init`) is left in place; it is node-level and reused.
