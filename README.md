# rucher

Multi-node manager for **podman Quadlet** workloads. It runs each workload group as an
isolated *cadre* — a directory of Quadlet units reconciled into rootless-podman services
under per-user systemd — and manages many nodes from one place. Each cadre is an isolated
environment backed by a dedicated Linux system user, its own podman secret and
registry-credential store, its own age identity, and (optionally) a systemd resource slice.

At its core is an idempotent single-node reconciler; on top of it, optional layers extend it
across many nodes — a pull-based GitOps agent (each node reconciles the cadres a
`placement.yml` assigns it, from a git or S3 store), an operator status plane that queries
every node over SSH, and per-cadre overlay networking. See [`docs/`](docs/) for the full
reference.

**New here?** Follow the [Quick start](docs/quickstart.md) — from install to a running
cadre in about five minutes.

## What it does

- You author Quadlet units (`.container`/`.volume`/`.network`/`.pod`/…) plus any support
  files (env files, configs) in a cadre directory. The tool does **not** generate
  units — you write them.
- `apply` lays the units + support files into the cadre user's
  `~/.config/containers/systemd/`, creates podman secrets, runs `systemctl --user
  daemon-reload`, and starts the services. It is **idempotent** and reconciles drift:
  changing one support file restarts only the units that use it.
- Secrets live **encrypted at rest** (SOPS + age) inside the cadre directory, safe
  to commit to a store. Plaintext is decrypted in memory and fed to podman over stdin —
  never written to disk, never passed on argv.

Native systemd gives dependencies (`After=`/`Requires=`), lifecycle hooks
(`ExecStartPre=`/…), and timers (`.timer`) for free within a cadre.

## Build

```bash
GOOS=linux go build -trimpath -ldflags="-s -w" -o rucher ./cmd/rucher
```

Runs as **root** on the target node (it creates users, manages linger/subuids, and drives
each user's systemd). Requires Go ≥ 1.26 to build; dependencies (age, go-git, minio-go,
`golang.org/x/crypto`, yaml) are pulled as Go modules. `GOOS=linux` cross-compiles to Linux
from any host; `GOARCH` defaults to your machine's — set it explicitly (e.g. `GOARCH=amd64`)
when the nodes' architecture differs. `-trimpath` and `-ldflags="-s -w"` strip filesystem
paths and the symbol/DWARF tables for a smaller, reproducible binary (~⅓ smaller).

## Node prerequisites

Debian (arm64/amd64) with: `podman` (rootless-capable), `uidmap`
(`newuidmap`/`newgidmap`), and systemd with `loginctl`/`runuser`. Run as root (or via
passwordless sudo). Secrets are decrypted **in-process**.

## Cadre layout

```
cadres/<name>/
  rucher.yml          # manifest
  secrets.sops.yaml        # SOPS+age, encrypted to THIS cadre's recipient (optional)
  web.container            # your Quadlet units
  nginx.conf  app.env      # support files referenced by the units
```

```yaml
# rucher.yml — the cadre's name is its directory name (no name field)
secrets:
  from: secrets.sops.yaml  # keys in this file become podman secrets
registries:
  login:
    - registry: ghcr.io
      username: deploy
      passwordKey: ghcr_token   # value taken from the sops file
resources:                 # optional -> systemd slice on the cadre user
  memoryMax: 512M
  cpuQuota: "50%"
```

Units reference support files by their in-place path, e.g.
`EnvironmentFile=%h/.config/containers/systemd/app.env`.

## Commands

```
rucher node cadre new <name>                  # create the user + age identity; print the recipient
rucher node cadre recipient <name>            # print a cadre's age recipient
rucher ops validate [--dir DIR] [name...]     # check manifests + unit files (no node)
rucher ops plan [--dir DIR] [name...]         # dry-run: show what apply would change
rucher node apply [--dir DIR]                 # reconcile cadres onto the node
rucher node cadre apply [--dir DIR] <name...> # reconcile the named cadre(s)
rucher node cadre status [name...]            # per-unit ActiveState/SubState
rucher node cadre logs <name> <unit>          # last journal lines for one unit
rucher node cadre rm <name> [--purge]         # stop + unmanage; --purge also deletes the user + data
rucher node key init|show                     # this node's age key (GitOps)
rucher ops key seal <name> --to <node-rcpt>   # seal a cadre identity to node(s)
rucher node agent run|install [--config P]    # pull-based reconcile from a git/S3 store
rucher ops nodes [--dir DIR] join <node> --address <addr>  # record a node's management address
rucher ops nodes [--dir DIR] status [--live] [--json] [--concurrency N]  # nodes status over SSH (parallel)
rucher ops nodes [--dir DIR] deploy [--store-url URL] [--concurrency N] <node...>  # provision + install rucher + bootstrap the agent
```

No `--dir` defaults to `./cadres`; no names means all cadres. Full reference:
[`docs/cli.md`](docs/cli.md).

## Secret workflow

```bash
sudo rucher node cadre new web                                   # prints age1... recipient
printf 'db_password: s3cr3t\n' \
  | rucher ops secrets encrypt --to <recipient> \
  > cadres/web/secrets.sops.yaml              # encrypt to that recipient
sudo rucher node cadre apply --dir ./cadres web            # decrypt + create podman secret + start
```

At apply time the root agent decrypts the SOPS file **in-process** using the cadre's age
identity (root can read both the file and the identity), then creates the podman secret and
any registry logins as the cadre user via stdin. Both `ops secrets encrypt` and the decrypt
path are byte-compatible with the `sops` CLI.

## On-node layout

| What | Path |
|------|------|
| Cadre user | `rucher-<name>` (system user, nologin) |
| Home | `/var/lib/rucher/cadres/<name>` |
| Units + support files | `<home>/.config/containers/systemd/` |
| age identity / recipient | `<home>/.config/rucher/age/` |
| Last-applied state (hashes only) | `/var/lib/rucher/cadres/state/<name>.json` |
| Resource slice drop-in | `/etc/systemd/system/user-<uid>.slice.d/50-rucher.conf` |

Each cadre user gets a unique, non-overlapping subuid/subgid block (allocated from
`/etc/subuid`), so many cadres coexist on one node.

## Host keys

`rucher ops nodes status` (and the rest of the operator control plane) reaches nodes with a
built-in Go SSH client. Host keys are trusted
**TOFU**: an unknown node is accepted and pinned on first contact into
`~/.config/rucher/known_hosts` (created mode 0600); a later key **change** for the
same node is rejected.

This is a separate trust store from `~/.ssh/known_hosts`, so every node re-pins on first
contact after switching to the native client. Lima nodes (previously reached via
`ssh -F ~/.lima/<name>/ssh.config`, which disables host-key checking) are now TOFU-pinned
like any other node. If a Lima VM is recreated on the **same** forwarded port with a new
key, clear its line from `~/.config/rucher/known_hosts` before reconnecting.

## Cadre overlays

Workloads can get transparent L3 mesh connectivity across nodes (a *cadre overlay*)
without any manager code change. It fits the opaque-Quadlet model: the operator authors a
kernel-mode Tailscale sidecar plus the app in one pod, and the auth key rides the existing
`secrets.create` machinery (podman secret → sidecar env). Privilege stays confined to the
sidecar; the unprivileged app shares the pod netns and reaches the tailnet transparently.
This is distinct from the operator control-plane network (`rucher ops nodes join`, which sets a
*node's* management address). See the ready example in
[`docs/examples/overlay-example/`](docs/examples/overlay-example/), the automated cross-node test
`test/integration/headscale_test.go`, and the real-tailnet validation record
[`docs/validation/integration-overlay.md`](docs/validation/integration-overlay.md).

## Testing

Pure logic and the shell-out layer are unit-tested with a fake command runner
(`go test ./...`). End-to-end behavior on real nodes is covered by an automated suite
(`go test -tags integration ./test/integration/`) driving Lima nodes — single-node core, the
GitOps agent (git + S3), the operator plane, cadre isolation, and a headscale overlay; see
[`test/integration/`](test/integration/). One manual record remains for the real-tailnet overlay
with direct kernel routing: [integration-overlay](docs/validation/integration-overlay.md).

## License

Licensed under the GNU Affero General Public License v3.0 or later
(`AGPL-3.0-or-later`) — see [`LICENSE`](LICENSE). Every source file carries an
`SPDX-License-Identifier` header.
