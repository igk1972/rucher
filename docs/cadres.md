# Cadres

A **cadre** is one workload group, defined by a directory and reconciled onto a node as
a dedicated rootless-podman environment owned by its own Linux user.

## Directory layout

```
cadres/<name>/
  rucher.yml          # manifest (required)
  secrets.sops.yaml        # SOPS+age, encrypted to THIS cadre's recipient (optional)
  identity.age             # sealed cadre identity (GitOps only; optional)
  web.container            # your Quadlet unit files
  nginx.conf  app.env      # support files referenced by the units
```

The tool classifies each entry in the directory:

- **Manifest** — `rucher.yml`. Parsed strictly (see below).
- **Service files** — never materialized onto the node: `rucher.yml`, the secrets file
  named by `secrets.from` (default `secrets.sops.yaml`), any `.sops.yaml`, and sealed
  identities matching `identity.*.age`.
- **Unit files** — files whose extension is one of `.container`, `.volume`, `.network`,
  `.pod`, `.kube`, `.image`, `.build` (what podman's Quadlet generator understands).
- **Support files** — everything else (env files, configs, …). Copied in as-is.

Unit and support files are laid into the cadre user's
`~/.config/containers/systemd/`. Units reference support files by their in-place path, e.g.
`EnvironmentFile=%h/.config/containers/systemd/app.env`.

## Manifest schema (`rucher.yml`)

Decoded strictly: an unknown key (e.g. a typo like `memmoryMax`) is a hard error rather than
being silently dropped.

```yaml
name: web                    # required; MUST equal the directory name
secrets:
  from: secrets.sops.yaml    # SOPS file whose keys are available (default: secrets.sops.yaml)
  create:                    # optional allowlist: only these keys become podman secrets.
    - db_password            # omit `create` entirely to turn EVERY decrypted key into a secret.
registries:
  login:                     # optional; performed as `podman login --password-stdin`
    - registry: ghcr.io
      username: deploy
      passwordKey: ghcr_token   # value taken from the decrypted SOPS file
      insecure: false           # optional; true adds --tls-verify=false
resources:                   # optional -> systemd slice drop-in on the cadre user
  memoryMax: 512M            # -> [Slice] MemoryMax=
  cpuQuota: "50%"            # -> [Slice] CPUQuota=
```

| Field | Type | Notes |
|-------|------|-------|
| `name` | string, required | Must equal the directory basename, else load fails. |
| `secrets.from` | string | SOPS file inside the directory; default `secrets.sops.yaml`. |
| `secrets.create` | list of strings | Keys to materialize as podman secrets. Empty/absent = all decrypted keys. A listed key absent from the SOPS file is an error. |
| `registries.login[].registry` | string, required | Registry host. |
| `registries.login[].username` | string, required | Login username. |
| `registries.login[].passwordKey` | string, required | Key in the SOPS file holding the password. |
| `registries.login[].insecure` | bool | Adds `--tls-verify=false`. |
| `resources.memoryMax` | string | systemd `MemoryMax=` value (any form systemd accepts). |
| `resources.cpuQuota` | string | systemd `CPUQuota=` value. |

### Validation at load

Beyond strict decode and the `name == directory` check, each unit file is validated: it must
contain at least one `[Section]` header, and any `EnvironmentFile=` pointing at a
cadre-local file (a bare filename, or a path under `%h/.config/containers/systemd/`)
must resolve to a file the cadre actually ships. Secret keys and resource-limit
formats are deliberately not validated at load (they need decrypted secrets / systemd's own
parsing). See [secrets.md](secrets.md).

## Per-cadre user and rootless isolation

Each cadre gets a dedicated Linux **system** user `rucher-<name>` with:

- a home at `/var/lib/rucher/cadres/<name>` and shell `/usr/sbin/nologin`;
- **linger** enabled (`loginctl enable-linger`) so `/run/user/<uid>` and the user's systemd
  manager persist across logins and across reboots;
- a **unique, non-overlapping subuid/subgid block** (65536 IDs, allocated above any existing
  range found in `/etc/subuid` + `/etc/subgid`), so many cadres coexist on one node
  with disjoint user-namespace mappings;
- its own podman secret store, registry credentials and age identity.

Commands run inside the user's session via `runuser -u <user> -- env XDG_RUNTIME_DIR=…
DBUS_SESSION_BUS_ADDRESS=…`, targeting the per-user systemd/DBus bus. Because privilege is a
plain Linux user boundary, a cadre's workloads cannot see another cadre's
secrets, volumes or processes.

## How `plan` and `apply` reconcile

Reconciliation diffs the desired cadre against the **last-applied state** — a
hashes-only JSON file at `/var/lib/rucher/cadres/state/<name>.json` recording
each file's content hash, each secret's value hash, the unit list, the uid and the resource
limits. `plan` computes the diff against an empty prior state (so it shows the full intended
change) and prints it; `apply` computes it against the real prior state and executes it.

The plan is a minimal, idempotent change set:

- **Files** — write files whose content hash changed or are new; remove files present in the
  prior state but no longer desired.
- **Secrets** — (re)create a podman secret when its value hash changed; remove secrets no
  longer present. See [secrets.md](secrets.md).
- **Resource limits** — re-apply the slice drop-in only when the limits changed.
- **daemon-reload** — scheduled when any unit file was written or removed.
- **Unit lifecycle**:
  - a new unit is **started**;
  - a unit whose own file changed is **restarted**;
  - a unit that references a changed support file or a changed secret (detected by scanning
    the unit for `EnvironmentFile`/`Volume`/`Mount`/`Secret`/`PodmanArgs`/… references) is
    **restarted** — so editing one config file restarts only the units that use it;
  - if a changed support file is referenced by no unit, all units are restarted as a
    conservative fallback;
  - a unit that disappeared is **stopped** (before its file is removed, while its generated
    `.service` still resolves).

`apply` executes in a fixed order: resource limits → stop removed units → write/remove files
→ create/remove secrets → registry logins → `daemon-reload` → start/restart units → persist
new state.

## On-node layout

| What | Path |
|------|------|
| Cadre user | `rucher-<name>` (system user, `nologin`) |
| Home | `/var/lib/rucher/cadres/<name>` |
| Units + support files | `<home>/.config/containers/systemd/` |
| age identity / recipient | `<home>/.config/rucher/age/{identity.txt,recipient.txt}` |
| Last-applied state (hashes only) | `/var/lib/rucher/cadres/state/<name>.json` |
| Resource slice drop-in | `/etc/systemd/system/user-<uid>.slice.d/50-rucher.conf` |

`RUCHER_STATE_DIR` overrides the base directory for the state file (useful for tests and
alternative layouts).
