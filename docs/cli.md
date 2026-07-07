# CLI reference

The binary is `rucher`. Invocation is `rucher <group> <command> [args]`. Commands are split into
two top-level groups:

```
node   on the Linux node — cadre lifecycle, the node's own key, and the GitOps agent (run as root)
ops    from the operator machine — plan, seal cadre keys, manage the nodes over SSH (any OS)
```

The `node` group shells out on the node (`runuser`/`systemctl`/`podman`) and its cadre lifecycle
commands run as **root** (they create users and drive per-user systemd). The `ops` group is
cross-platform. Unknown commands and missing required arguments print a usage line and exit
non-zero.

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
    ├─ plan   [--dir DIR] [name...]                    [local]  dry-run: what apply would change
    ├─ nodes [--dir DIR]
    │   ├─ status [--live] [--json] [node...]          [ssh]    gather nodes status over SSH
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

### `--dir DIR` for cadres (node apply, node cadre apply, ops plan)

`--dir` is the **parent** directory whose immediate subdirectories are cadres; the
subdirectory name is the cadre name. It defaults to `./cadres`. `node apply` reconciles
**every** cadre under `--dir` and takes no positional names (use `node cadre apply
<name...>` for specific ones). `ops plan` takes optional positional names — with none,
every subdirectory is selected; `node cadre apply` requires at least one. A requested name
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
the user's unit (`_SYSTEMD_USER_UNIT` + `_UID`), because a nologin system user cannot open
its own `journalctl --user`. `<unit>` is the Quadlet filename (e.g. `web.container`).

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

Runs from the operator machine (any OS). `plan` is a read-only dry run; `key seal` seals a
cadre identity to node(s); `secrets encrypt` encrypts a cadre's secrets in-process; `nodes`
reaches every node over SSH.

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

### `rucher ops secrets encrypt --to <recipient> [--to <recipient> ...]`

Read a flat `key: value` YAML map on stdin and write the encrypted `secrets.sops.yaml`
(SOPS+age) to stdout, encrypted to every `--to` recipient (repeated values de-duplicated).
This is the in-process replacement for `sops --encrypt --age <recipient>`; the output is
byte-compatible with the `sops` CLI. See [secrets.md](secrets.md).

```bash
printf 'db_password: s3cr3t\n' \
  | rucher ops secrets encrypt --to <cadre-recipient> \
  > cadres/web/secrets.sops.yaml
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

### `rucher ops nodes [--dir DIR] status [--live] [--json] [node...]`

Gather each node's agent status over SSH and print it. Default output is a table
(`NODE ADDRESS REACHABLE REVISION APPLIED REMOVED ERRORS`) followed by an errors detail
block; `--json` emits a JSON array instead. `--live` additionally runs `rucher node cadre status`
on each reachable node and appends the live per-unit output. With no node names, every node under
`--dir` that has a `configuration.yml` is queried. Exit code is 1 if any node is
unreachable. See [management-network.md](management-network.md).

```bash
rucher ops nodes status
rucher ops nodes status --live node-a
rucher ops nodes status --json
```

### `rucher ops nodes [--dir DIR] deploy [--version TAG | --binary PATH] [store flags] [--json] [node...]`

Install/update rucher on the named nodes over SSH and bootstrap them, from the operator.
For each node (all under `--dir` when none named): probe its architecture, **provision the
base platform idempotently** (static podman if absent, `uidmap`, `/dev/net/tun`), deliver
the `rucher` binary to `/usr/local/bin/rucher`, run `node key init` (printing the node's age
recipient), and — when a store is configured — write `/etc/rucher/agent.yml` and run
`node agent install` (systemd timer).

Binary source: by default the node downloads `rucher_linux_<arch>` from the GitHub Release
(`--version <tag>`, else `latest`; `--repo <owner/repo>` defaults to `igk1972/rucher`).
`--binary <path>` uploads a local linux binary instead (dev).

Agent bootstrap turns on when a store is given: `--store-url <url>` (git) or `--store-bucket`
(s3), with `--store-kind git|s3` (default git), `--store-branch` (default main),
`--interval` (default 30s), and auth passthroughs (`--store-ssh-key`, `--store-token`,
`--store-insecure-host-key`; S3: `--store-endpoint/-prefix/-access-key/-secret-key/-region`,
`--store-ssl`). Without a store, deploy stops after the binary + `node key init`.

Output is a table (`NODE ADDRESS ARCH AGENT RECIPIENT OK`) with per-node errors below, or a
JSON array with `--json`. Exit code is 1 if any node failed.

```bash
# download latest release, provision + bootstrap the git-store agent on two nodes:
rucher ops nodes deploy --store-url git@example.com:store.git node-a node-b

# pin a version; binary + key init only (no agent):
rucher ops nodes deploy --version v0.1.0 node-a

# push a locally built binary (dev):
rucher ops nodes deploy --binary ./rucher node-a
```
