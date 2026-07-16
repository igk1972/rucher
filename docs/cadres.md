# Cadres

A **cadre** is one workload group, defined by a directory and reconciled onto a node as
a dedicated rootless-podman environment owned by its own Linux user. Its name is the
directory's name — the manifest carries no name field.

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
- **Systemd units** — `.timer`, `.socket`, `.path`, `.service`: native systemd units the cadre
  ships itself. A `.timer`/`.socket`/`.path` schedules or activates a service (e.g. a `.timer`
  firing a generated `.service`). A `.service` lets a cadre supply its own unit — a oneshot
  fired by a companion `.timer`/`.socket`/`.path` (like the synthesized prune), or a standalone
  service. A cadre `.service` must not be named after the `.service` Quadlet generates from one
  of the cadre's units (e.g. `web.container` → `web.service`), which it would otherwise shadow.
- **Support files** — everything else (env files, configs, …). Copied in as-is.

Quadlet units and support files are laid into the cadre user's
`~/.config/containers/systemd/`; native systemd units go to `~/.config/systemd/user/`
(where systemd's user manager looks for them). A `.timer`/`.socket`/`.path` — and a `.service`
carrying an `[Install]` section — is enabled directly; an `[Install]`-less `.service` is
installed and daemon-reloaded but not enabled, left for its companion unit to activate. Units
reference support files by their in-place path, e.g.
`EnvironmentFile=%h/.config/containers/systemd/app.env`.

## Manifest schema (`rucher.yml`)

Decoded strictly: an unknown key (e.g. a typo like `memmoryMax`) is a hard error rather than
being silently dropped.

```yaml
# The cadre's name is its directory name; the manifest has no name field.
secrets:
  from: secrets.sops.yaml    # SOPS file whose keys are available (default: secrets.sops.yaml)
  create:                    # optional allowlist: only these keys become podman secrets.
    - db_password            # omit `create` to turn every non-empty decrypted key into a secret.
registries:
  login:                     # optional; performed as `podman login --password-stdin`
    - registry: ghcr.io
      username: deploy
      passwordKey: ghcr_token   # value taken from the decrypted SOPS file
      insecure: false           # optional; true adds --tls-verify=false
resources:                   # optional -> systemd slice drop-in on the cadre user
  memoryMax: 512M            # -> [Slice] MemoryMax=
  cpuQuota: "50%"            # -> [Slice] CPUQuota=
prune:                       # optional; synthesized image GC (default: enabled)
  enabled: true              # false disables GC and removes the synthesized units
  schedule: daily            # systemd OnCalendar= expression
  until: 168h                # prune unused images created earlier than this (Go duration)
```

| Field | Type | Notes |
|-------|------|-------|
| `secrets.from` | string | SOPS file inside the directory; default `secrets.sops.yaml`. |
| `secrets.create` | list of strings | Keys to materialize as podman secrets. Empty/absent = all decrypted keys **with a non-empty value** (an empty-valued key is skipped unless explicitly listed). A listed key absent from the SOPS file is an error. |
| `registries.login[].registry` | string, required | Registry host. |
| `registries.login[].username` | string, required | Login username. |
| `registries.login[].passwordKey` | string, required | Key in the SOPS file holding the password. |
| `registries.login[].insecure` | bool | Adds `--tls-verify=false`. |
| `resources.memoryMax` | string | systemd `MemoryMax=` value (byte size, percentage, or `infinity`); validated. |
| `resources.cpuQuota` | string | systemd `CPUQuota=` value (a percentage); validated. |
| `prune.enabled` | bool | Default `true`. `false` disables image GC and removes the synthesized units. |
| `prune.schedule` | string | systemd `OnCalendar=` expression; default `daily`. |
| `prune.until` | string | Go duration; unused images **created** earlier than this are pruned; default `168h`. |

### Validation at load

Beyond strict decode, each unit file is validated: it must
contain at least one `[Section]` header, a Quadlet file must contain its type section
(`[Container]` for `.container`, `[Volume]` for `.volume`, … — the generator fails
without it), and any `EnvironmentFile=` pointing at a
cadre-local file (a bare filename, or a path under `%h/.config/containers/systemd/`)
must resolve to a file the cadre actually ships. Secret keys are deliberately not validated
at load (they need the decrypted secrets); `resources.memoryMax`/`cpuQuota` formats, by
contrast, **are** checked at load. See [secrets.md](secrets.md). `ops validate` additionally runs each Quadlet unit
through Podman's own parser to catch unknown keys / missing `Image=` / bad values before
they reach a node (see the `ops validate` section of [cli.md](cli.md)).

Two file names are reserved for the synthesized image-GC units (see below): a cadre that
ships its own `rucher-prune.service` or `rucher-prune.timer` fails to load.

## Image garbage collection

With floating tags (`:latest`) every re-pull leaves the previous image behind as an unused
layer; without cleanup the cadre's storage (`<home>/.local/share/containers/storage/`)
eventually fills the disk. So every cadre gets a pair of **synthesized units** —
`rucher-prune.service` (a oneshot running `podman image prune --all --force --filter
until=<until>`) and `rucher-prune.timer` — installed into `~/.config/systemd/user/` and
enabled **by default**, configured by the manifest `prune:` block.

- The prune removes **unused** images **created** more than `until` ago. `until` is creation
  age, not last-use age; images used by existing containers are never removed.
- The timer runs with `Persistent=true` (a window missed while the node was down is caught up
  on boot) and a randomized delay, so the cadres of one node don't all prune at once. Right
  after the first enable the timer may fire immediately — a no-op on a fresh cadre.
- The units follow the manifest through the normal reconcile: changing `schedule`/`until`
  updates them on the next apply; `prune.enabled: false` disables the timer and removes both
  files. `node cadre status` lists `rucher-prune.timer` alongside the cadre's own units.

## Per-cadre user and rootless isolation

Each cadre gets a dedicated Linux user `rucher-<name>` (a regular login user with a
`nologin` shell, not a `--system` account) with:

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
secrets, volumes or processes. Network visibility has its own rules — see below.

## Network isolation

Each cadre's containers run in their own **network namespaces** under the cadre's user
(rootless podman networking; pasta on the reference stack). There is no shared bridge
between cadres, and a container has no route into another cadre's namespace:
**a service that is not published is unreachable from other cadres** — and from anywhere
else.

Publishing a port (`PublishPort=`) is the only doorway, and it has two distinct audiences:

- **The outside network.** The bind address works as expected: `PublishPort=8080:80` (no
  host address) or `0.0.0.0:…` accepts connections from other machines;
  `PublishPort=127.0.0.1:<host>:<ctr>` is invisible to them.
- **Neighbouring cadres on the same node.** Every published port — **including one bound
  to `127.0.0.1`** — is reachable from a co-located cadre. Rootless networking maps the
  container's default gateway address to the host (pasta `--map-gw`), and such connections
  arrive over the host's loopback, so a neighbour reaches `<gateway>:<port>` regardless of
  the bind address. Validated empirically on the reference stack (Debian, podman 5.4,
  pasta): a `127.0.0.1`-bound nginx answered a neighbour cadre's request to the gateway
  address, while an unpublished port stayed unreachable.

Rules of thumb:

- **Never publish on all interfaces unless the service is meant to be public.**
  `ops validate` warns about it (see [cli.md](cli.md)). For a service consumed by a
  host-local reverse proxy (nginx/Traefik running on the node), publish on loopback:
  `PublishPort=127.0.0.1:<host>:<ctr>` — the proxy reaches it, the outside network cannot.
- **Treat any published port as visible to every cadre on its node.** If co-located cadres
  must not reach a service, don't publish it: keep the traffic inside the pod (containers
  of one pod share a network namespace), use an authenticated overlay
  (see [overlays.md](overlays.md)), or make the service itself authenticate.

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
- **Systemd units** (`.timer`/`.socket`/`.path`, and a `.service` with `[Install]`): a new one
  is **`enable --now`**'d (so it also persists across reboot under linger), a changed one is
  **restarted**, and a removed one is **`disable --now`**'d before its file is deleted. An
  `[Install]`-less `.service` is only written and daemon-reloaded — never enabled or restarted;
  a change takes effect the next time its companion unit fires it.
- **Synthesized prune units** — the desired file set additionally contains the image-GC units
  generated from the manifest's `prune:` block (unless disabled), so the same diff writes,
  enables, updates and removes them. The `.timer` gets the enable/restart lifecycle; the
  `[Install]`-less `.service` is only written — a change takes effect at the timer's next fire.

`apply` executes in a fixed order: resource limits → stop removed units → write/remove files
→ create/remove secrets → registry logins → `daemon-reload` → start/restart/enable units →
persist new state.

## On-node layout

| What | Path |
|------|------|
| Cadre user | `rucher-<name>` (`nologin`) |
| Home | `/var/lib/rucher/cadres/<name>` |
| Quadlet units + support files | `<home>/.config/containers/systemd/` |
| Native systemd units (`.timer`/`.socket`/`.path`/`.service`) + synthesized prune units | `<home>/.config/systemd/user/` |
| age identity / recipient | `<home>/.config/rucher/age/{identity.txt,recipient.txt}` |
| Last-applied state (hashes only) | `/var/lib/rucher/cadres/state/<name>.json` |
| Resource slice drop-in | `/etc/systemd/system/user-<uid>.slice.d/50-rucher.conf` |

`RUCHER_STATE_DIR` overrides the base directory for the state file (useful for tests and
alternative layouts).
