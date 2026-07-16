# CLI reference

The binary is `rucher`. Invocation is `rucher <group> <command> [args]`. Commands are split into
two top-level groups:

```
node   on the Linux node — cadre lifecycle, the node's own key, and the GitOps agent (run as root)
ops    from the operator machine — plan, seal cadre keys, manage the nodes over SSH (any OS)
```

The `node` group shells out on the node (`runuser`/`systemctl`/`podman`) and its cadre lifecycle
commands run as **root** (they create users and drive per-user systemd). The `ops` group is
cross-platform. Unknown commands, unrecognized flags, and missing or extra arguments print a
usage line and exit non-zero.

`-h`, `--help`, and `help` are help requests, not errors: at each command **group** they print
that group's usage and exit **0**. `rucher --help` prints the full command map; a group scopes it
(`rucher node --help`, `rucher ops nodes --help`, `rucher node cadre --help`, `rucher ops key --help`).
A leaf verb (`ops nodes status`, `node cadre logs`, …) does not take `--help` — see its syntax in
the command map above.

## Command map

```
rucher <group> <command> [args]
│
├─ node                                                on the Linux node (runuser/systemctl/podman)
│   ├─ apply   [--dir DIR]                             [node]   reconcile ALL cadres under --dir
│   ├─ cadre
│   │   ├─ new <name>                                  [node]   create the user + age identity, print the recipient
│   │   ├─ apply  [--dir DIR] <name...>                [node]   reconcile the named cadre(s)
│   │   ├─ status [name...]                            [node]   ActiveState/SubState per unit
│   │   ├─ logs <name> <unit>                          [node]   last 200 journal lines for one unit
│   │   ├─ rm <name> [--purge]                         [node]   unmanage; --purge also deletes the user + data
│   │   └─ recipient <name>                            [node]   print the cadre's age recipient
│   ├─ key
│   │   ├─ init                                        [node]   create /etc/rucher/node/identity.txt, print the recipient
│   │   └─ show                                        [node]   print the node recipient
│   └─ agent
│       ├─ run     [--config PATH]                     [node]   one pull-based reconcile pass
│       └─ install [--config PATH]                     [node]   write + enable the systemd service + timer
└─ ops                                                 from the operator machine
    ├─ init [--dir DIR] <name>                         [local]  scaffold a cadre directory
    ├─ validate [--dir DIR] [name...]                  [local]  check cadre manifests + unit files
    ├─ plan   [--dir DIR] [name...]                    [local]  dry-run: what apply would change
    ├─ nodes [--dir DIR]
    │   ├─ status [--live] [--json] [--concurrency N] [node...]  [ssh]  gather nodes status over SSH
    │   ├─ join <node> --address <addr> [--json]       [local]  record a node's management address
    │   └─ deploy [--version TAG|--binary PATH] [...]  [ssh]    provision + install rucher + bootstrap the agent
    ├─ key
    │   └─ seal <name> --to <rcpt> [--to <rcpt> ...]   [local]  seal a cadre identity to node(s)
    └─ secrets
        └─ encrypt --to <rcpt> [--to <rcpt> ...]       [local]  encrypt plaintext YAML (stdin) to a SOPS+age file
```

Execution side: `[node]` shells out on the local machine (`runuser`/`systemctl`/`podman`) — the
machine must be a Linux node; `[local]` touches only local files/crypto — any OS; `[ssh]` reaches
remote nodes (the client is cross-platform). Defaults: `--dir` is `./cadres` for cadre commands
and `./nodes` for `ops nodes`; `--config /etc/rucher/agent.yml`. Exit codes: `0` ok, `1` runtime error, `2` usage/parse error.

## Shared conventions

### `--dir DIR` for cadres (node apply, node cadre apply, ops validate, ops plan)

`--dir` is the **parent** directory whose immediate subdirectories are cadres; the
subdirectory name is the cadre name. It defaults to `./cadres`. `node apply` reconciles
**every** cadre under `--dir` and takes no positional names (use `node cadre apply
<name...>` for specific ones). `ops validate` and `ops plan` take optional positional names —
with none, every subdirectory is selected; `node cadre apply` requires at least one. A requested name
that is not a subdirectory of `--dir` is an error (this guards against pointing `--dir` at a
single cadre folder instead of its parent).

```bash
rucher node cadre apply --dir ./cadres web   # reconcile ./cadres/web
rucher node apply --dir .                            # reconcile every cadre under .
```

### `--dir DIR` for node configs (ops nodes)

For `ops nodes`, `--dir` points at the directory of per-node config folders
(`<DIR>/<node>/configuration.yml`).
It defaults to `./nodes`. See [management-network.md](management-network.md).

```bash
rucher ops nodes --dir ./nodes status
```

---

## node

Runs on the Linux node. The `cadre` subgroup manages the cadre lifecycle; `key` manages the
node's own age key; `agent` drives the pull-based GitOps agent. `node apply` reconciles **all**
cadres under `--dir`; `node cadre apply <name...>` reconciles **only the named** cadre(s).

### `rucher node apply [--dir DIR]`

Reconcile **every** cadre under `--dir` onto the node: for each one ensure the user, decrypt
secrets, diff against the last-applied state, and apply the minimal changes (write files, create
secrets, registry logins, resource limits, `daemon-reload`, start/restart/stop units). Idempotent.
Prints `started=<n> restarted=<n>` per cadre. It takes no positional names — for a single
cadre use `node cadre apply <name>`.

```bash
sudo rucher node apply --dir ./cadres
```

### `rucher node cadre new <name>`

Provision a cadre's OS user (`rucher-<name>`) and its age identity if absent, then print
the cadre's age recipient. Idempotent: re-running returns the existing recipient.

```bash
sudo rucher node cadre new web        # -> age1... (the cadre's recipient)
```

### `rucher node cadre apply [--dir DIR] <name...>`

Reconcile the **named** cadre(s) onto the node — same reconcile as `node apply`, but scoped
to the cadres you name (at least one required): ensure the user, decrypt secrets, diff
against the last-applied state, and apply the minimal changes. Idempotent. Prints
`started=<n> restarted=<n>` per cadre.

```bash
sudo rucher node cadre apply --dir ./cadres web
```

### `rucher node cadre status [name...]`

Print each cadre's per-unit `ActiveState`/`SubState` (via `systemctl --user show`) as a
table. With no names it reports every cadre that has a persisted state file. (Note:
`status` does not take `--dir`; it works from persisted state, not a directory.)

```bash
sudo rucher node cadre status
sudo rucher node cadre status web
```

### `rucher node cadre logs <name> <unit>`

Print the last 200 journal lines for one of a cadre's units. Read as root filtered to
the cadre user's unit (`_SYSTEMD_USER_UNIT` + `_UID`), which works regardless of which
journal file holds them. `<unit>` is the Quadlet filename (e.g. `web.container`).

```bash
sudo rucher node cadre logs web web.container
```

### `rucher node cadre rm <name> [--purge]`

Unmanage a cadre: stop its units, delete their unit files (so nothing restarts on
boot), and drop the state file. The OS user, its podman secrets/volumes and the age identity
are **kept**. With `--purge` it additionally tears down the OS user and its home (terminates
the user's session and processes, `userdel -r`, removes the resource slice drop-in).

```bash
sudo rucher node cadre rm web              # unmanage, keep the user and data
sudo rucher node cadre rm web --purge      # also delete the user + home + data
```

### `rucher node cadre recipient <name>`

Print a cadre's stored age recipient (used to encrypt its `secrets.sops.yaml`). See
[secrets.md](secrets.md).

```bash
sudo rucher node cadre recipient web    # -> age1...
```

### `rucher node key init` / `rucher node key show`

Manage the node's own age key (born on the node at `/etc/rucher/node/identity.txt`,
mode 0600; the private key never leaves the node). `init` creates it on first use and prints
its recipient; `show` prints the recipient of the existing key. Used by the GitOps
agent to unseal cadre identities. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher node key init         # -> age1... (this node's recipient)
sudo rucher node key show
```

### `rucher node agent run [--config PATH]` / `rucher node agent install [--config PATH]`

`run` performs one pull-based reconcile pass from the store described by the agent config
(default `/etc/rucher/agent.yml`); `install` writes a systemd oneshot service + timer
that run `node agent run` periodically and enables the timer. `run` prints
`revision <rev>: applied=<n> removed=<n>`; `install` prints `installed rucher-agent.timer`.
`--config`, when given, must come immediately after `run`/`install`. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher node agent run --config /etc/rucher/agent.yml
sudo rucher node agent install
```

## ops

Runs from the operator machine (any OS). `validate` statically checks cadre definitions;
`plan` is a read-only dry run; `key seal` seals a cadre identity to node(s); `secrets encrypt`
encrypts a cadre's secrets in-process; `nodes` reaches every node over SSH.

### `rucher ops init [--dir DIR] <name>`

Scaffold a new cadre directory `<dir>/<name>` (default `./cadres/<name>`): a fully
commented `rucher.yml` (an empty manifest is valid — the comments show the optional
`secrets`/`registries`/`resources`/`prune` blocks) and a minimal working `web.container`
(`nginx:alpine` publishing `127.0.0.1:8080:80` — pinned to loopback, so the scaffold
passes `validate` with no warnings). The name must match `[a-z0-9][a-z0-9-]*` and be at
most 25 characters (it becomes the node user `rucher-<name>`). Refuses to touch an
existing directory.

```bash
rucher ops init hello                       # -> ./cadres/hello/{rucher.yml,web.container}
rucher ops validate hello && rucher ops plan hello
```

### `rucher ops validate [--dir DIR] [name...]`

Statically check each selected cadre without touching a node — a fast pre-commit gate. It
loads every cadre and reports the first structural problem in each: a manifest that fails
strict decode (an unknown/misspelled key like `memmoryMax`) or `manifest.Validate`, a unit
file with no `[Section]` header, a Quadlet file missing its type section (`[Container]` in
a `.container`, `[Volume]` in a `.volume`, …), a unit whose `EnvironmentFile=` points at
a cadre-local file the directory does not ship, or a cadre `.service` named after the
`.service` Quadlet generates from one of the cadre's units (which it would otherwise shadow). Prints `<name>: OK` or
`<name>: ERROR <reason>` per cadre; exits `0` only when all pass, `1` if any fail.

Each Quadlet unit's **contents** are also checked with **Podman's own parser** (the same
`quadlet` code that generates the `.service` on the node, pinned to podman v6): an unknown
key (`Memoryyy=…`), a missing `Image=`, an invalid value, or a dangling cross-reference
(`Volume=x.volume` / `Network=x.network` / `Pod=x.pod` with no matching unit in the cadre)
is an `ERROR`. Limits: a plain external volume/network name (`pgdata:/data`, `Network=host`)
is not checked, `PublishPort=` format is parsed by podman later (only `ExposeHostPort=` is
caught here), and because the parser is pinned to one podman version it may diverge from a
node running a different one — treat it as a strong pre-commit gate, not the final word.

Advisory findings are printed as `<name>: WARN <reason>` lines and do **not** affect the
exit code. Currently one check: a `PublishPort=` that binds all interfaces (no host
address, `0.0.0.0`, or `[::]`) — that exposes the service to the outside network. Pin it
to `PublishPort=127.0.0.1:<host>:<ctr>` unless the service is meant to be public; note
that any published port stays reachable by co-located cadres regardless of the bind
address (see [network isolation](cadres.md#network-isolation)). Only the
`PublishPort=` key is inspected; ports hidden in `PodmanArgs=` are not.

It deliberately does **not** check secret keys or resource-limit formats — those need
decrypted secrets and systemd's own parsing, so they are validated later (see
[cadres.md](cadres.md) and [secrets.md](secrets.md)).

```bash
rucher ops validate --dir ./cadres          # all cadres
rucher ops validate --dir ./cadres web      # just one
```

### `rucher ops plan [--dir DIR] [name...]`

Dry run. For each selected cadre, load and validate it and print what `apply` would do
(against an empty prior state, so the full intended change is shown): units to start/restart
and files to write. Read-only — it touches nothing on the node and does not require root.

```bash
rucher ops plan --dir ./cadres web
```

### `rucher ops key seal <name> --to <node-recipient> [--to <node-recipient> ...]`

Generate a cadre keypair, seal its private identity to every listed node recipient (so
any of those nodes can unseal it), write the sealed `identity.age` into
`./cadres/<name>/`, and print the cadre's recipient. Repeated `--to` values are
de-duplicated. This is an operator-side command used when building the store. See
[gitops-agent.md](gitops-agent.md).

```bash
rucher ops key seal web --to age1nodeA... --to age1nodeB...   # -> web's recipient
```

### `rucher ops secrets encrypt [--to <rcpt> ... | --cadre <name> --seal-to <node-rcpt> ...] [--dir DIR] [--in FILE] [--out FILE]`

Encrypt a flat `key: value` YAML map to SOPS+age — byte-compatible with the `sops` CLI, the
in-process replacement for `sops --encrypt`. Two modes:

- **direct** — `--to <recipient>` (repeatable): encrypt to each recipient. Plaintext on
  stdin, encrypted document on stdout.
- **seal** — `--cadre <name> --seal-to <node-recipient>` (repeatable): generate the cadre's
  age identity, seal it to the node recipient(s) into `<dir>/<name>/identity.age`, encrypt to
  that identity, and write `<dir>/<name>/secrets.sops.yaml`. One command, no shell glue; it
  prints the cadre's recipient. `--dir` defaults to `cadres`.

`--in` and `--out` are both **optional**: plaintext comes from `--in FILE` (else stdin);
output goes to `--out FILE` (else stdout, or the cadre's `secrets.sops.yaml` in seal mode).
See [secrets.md](secrets.md) and [gitops-agent.md](gitops-agent.md).

```bash
# direct: encrypt to a known recipient (stdin -> stdout)
printf 'db_password: s3cr3t\n' | rucher ops secrets encrypt --to <cadre-recipient> > cadres/web/secrets.sops.yaml

# seal: generate + seal the cadre identity to a node and encrypt, in one command
rucher ops secrets encrypt --cadre web --seal-to <node-recipient> --in web.plain.yaml
```

### `rucher ops nodes [--dir DIR] join <node> --address <addr> [--json]`

Record `<node>`'s static management address into `<nodes-dir>/<node>/configuration.yml` as a
`network: {address: <addr>}` block, preserving other keys and comments. The node directory
must already exist. `--address` is required and non-empty; `--json` switches the success
output to a compact JSON object. See [management-network.md](management-network.md).

```bash
rucher ops nodes join node-a --address 100.64.0.1
rucher ops nodes join node-a --address 100.64.0.1 --json
```

### `rucher ops nodes [--dir DIR] status [--live] [--json] [--concurrency N] [node...]`

Gather each node's agent status over SSH and print it. Default output is a table
(`NODE ADDRESS REACHABLE REVISION APPLIED REMOVED ERRORS`) followed by an errors detail
block; `--json` emits a JSON array instead. `--live` additionally runs `rucher node cadre status`
on each reachable node and appends the live per-unit output. With no node names, every node under
`--dir` that has a `configuration.yml` is queried.

A node reached over SSH but whose agent has not written a status file yet — a freshly deployed node,
or a push-mode fleet driven by `node cadre apply` with no pull agent — is reported as **reachable
but pending**: `REACHABLE=yes` with `pending` in the REVISION column (JSON: `"pending": true` with an
empty `revision`). Pending is not a failure and does not affect the exit code; JSON consumers must
read the `pending` field rather than infer health from an empty `revision`. Exit code is 1 if any
node is unreachable or reported errors (a reachable node whose reconcile pass failed, or whose status
file could not be read for a reason other than "not written yet").
See [management-network.md](management-network.md).

Nodes are queried in parallel, at most `--concurrency` at a time (default 8; must be `>= 1`).
The output order always matches the node list, independent of the concurrency level.

```bash
rucher ops nodes status
rucher ops nodes status --live node-a
rucher ops nodes status --json
```

### `rucher ops nodes [--dir DIR] deploy [--version TAG | --binary PATH] [--repo OWNER/REPO] [store flags] [--concurrency N] [--json] [node...]`

Install/update rucher on the named nodes over SSH and bootstrap them, from the operator.
For each node (all under `--dir` when none named): probe its architecture, **provision the
base platform idempotently** (podman if absent, `uidmap`, `/dev/net/tun`), deliver
the `rucher` binary to `/usr/local/bin/rucher`, run `node key init` (printing the node's age
recipient), and — when a store is configured — write `/etc/rucher/agent.yml` and run
`node agent install` (systemd timer).

Binary source: by default the node downloads `rucher_linux_<arch>` from the GitHub Release
(`--version <tag>`, else `latest`; `--repo <owner/repo>` defaults to `igk1972/rucher`).
`--binary <path>` uploads a local linux binary instead (dev).

podman: when a node has no podman, deploy installs the distro (apt) podman by default — a
journald-capable build. `--podman-prebuilt` instead installs prebuilt podman 6.x `.deb` from
[`igk1972/podman-6-deb`](https://github.com/igk1972/podman-6-deb)'s Release (latest by default;
`--podman-version <tag>` pins one, e.g. `--podman-version v6.0.1`, and requires
`--podman-prebuilt`). The choice can also live in the node's `configuration.yml`
(`podman.source: apt|prebuilt`, `podman.version`); CLI flags override it. A node that already
has podman is left untouched.

Agent bootstrap turns on when a store is given: `--store-url <url>` (git) or `--store-bucket`
(s3), with `--store-kind git|s3` (default git), `--store-branch` (default main),
`--interval` (default 30s), and auth passthroughs (`--store-ssh-key`, `--store-token`, `--store-user`,
`--store-insecure-host-key`; S3: `--store-endpoint/-prefix/-access-key/-secret-key/-region`).
The S3 store uses TLS by default (`--store-ssl` states this explicitly); pass `--store-no-ssl`
(or set `useSSL: false` in the agent config) for a trusted plaintext endpoint. Without a store, deploy stops after the binary + `node key init`.

Nodes deploy in parallel, at most `--concurrency` at a time (default 4 — lower than `status`
because each deploy is heavy: base-platform provisioning and multi-MB binary transfers; must be
`>= 1`). The output order always matches the node list.

Output is a table (`NODE ADDRESS ARCH AGENT RECIPIENT OK`) with per-node errors below, or a
JSON array with `--json`. Exit code is 1 if any node failed.

```bash
# download latest release, provision + bootstrap the git-store agent on two nodes:
rucher ops nodes deploy --store-url git@example.com:store.git node-a node-b

# pin a version; binary + key init only (no agent):
rucher ops nodes deploy --version v0.1.0 node-a

# install prebuilt podman 6.x (instead of distro apt) on fresh nodes, pinned to a release:
rucher ops nodes deploy --podman-prebuilt --podman-version v6.0.1 node-a

# push a locally built binary (dev):
rucher ops nodes deploy --binary ./rucher node-a
```
