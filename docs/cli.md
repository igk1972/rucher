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
    │   └─ join <node> --address <addr> [--json]       [local]  record a node's management address
    └─ key
        └─ seal <name> --to <rcpt> [--to <rcpt> ...]   [local]  seal a cadre identity to node(s)
```

Execution side: `[node]` shells out on the local machine (`runuser`/`systemctl`/`podman`) — the
machine must be a Linux node; `[local]` touches only local files/crypto — any OS; `[ssh]` reaches
remote nodes (the client is cross-platform). Defaults: `--dir ./compartments`, `--dir ./nodes`,
`--config /etc/rucher/agent.yml`. Exit codes: `0` ok, `1` runtime error, `2` usage/parse error.

## Shared conventions

### `--dir DIR` for compartments (node apply, node cadre apply, ops plan)

`--dir` is the **parent** directory whose immediate subdirectories are compartments; the
subdirectory name is the compartment name. It defaults to `./compartments`. `node apply` and
`ops plan` take positional compartment names to act on; with none, every subdirectory is
selected. `node cadre apply` requires at least one name. A requested name that is not a
subdirectory of `--dir` is an error (this guards against pointing `--dir` at a single compartment
folder instead of its parent).

```bash
rucher node cadre apply --dir ./compartments web   # reconcile ./compartments/web
rucher node apply --dir .                            # reconcile every compartment under .
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

Runs on the Linux node. The `cadre` subgroup manages the compartment lifecycle; `key` manages the
node's own age key; `agent` drives the pull-based GitOps agent. `node apply` reconciles **all**
cadres under `--dir`; `node cadre apply <name...>` reconciles **only the named** cadre(s).

### `rucher node apply [--dir DIR]`

Reconcile **every** compartment under `--dir` onto the node: for each one ensure the user, decrypt
secrets, diff against the last-applied state, and apply the minimal changes (write files, create
secrets, registry logins, resource limits, `daemon-reload`, start/restart/stop units). Idempotent.
Prints `started=<n> restarted=<n>` per compartment. Positional names, if given, narrow the set.

```bash
sudo rucher node apply --dir ./compartments
```

### `rucher node cadre new <name>`

Provision a compartment's OS user (`rucher-<name>`) and its age identity if absent, then print
the compartment's age recipient. Idempotent: re-running returns the existing recipient.

```bash
sudo rucher node cadre new web        # -> age1... (the compartment's recipient)
```

### `rucher node cadre apply [--dir DIR] <name...>`

Reconcile the **named** compartment(s) onto the node — same reconcile as `node apply`, but scoped
to the compartments you name (at least one required): ensure the user, decrypt secrets, diff
against the last-applied state, and apply the minimal changes. Idempotent. Prints
`started=<n> restarted=<n>` per compartment.

```bash
sudo rucher node cadre apply --dir ./compartments web
```

### `rucher node cadre status [name...]`

Print each compartment's per-unit `ActiveState`/`SubState` (via `systemctl --user show`) as a
table. With no names it reports every compartment that has a persisted state file. (Note:
`status` does not take `--dir`; it works from persisted state, not a directory.)

```bash
sudo rucher node cadre status
sudo rucher node cadre status web
```

### `rucher node cadre logs <name> <unit>`

Print the last 200 journal lines for one of a compartment's units. Read as root filtered to
the user's unit (`_SYSTEMD_USER_UNIT` + `_UID`), because a nologin system user cannot open
its own `journalctl --user`. `<unit>` is the Quadlet filename (e.g. `web.container`).

```bash
sudo rucher node cadre logs web web.container
```

### `rucher node cadre rm <name> [--purge]`

Unmanage a compartment: stop its units, delete their unit files (so nothing restarts on
boot), and drop the state file. The OS user, its podman secrets/volumes and the age identity
are **kept**. With `--purge` it additionally tears down the OS user and its home (terminates
the user's session and processes, `userdel -r`, removes the resource slice drop-in).

```bash
sudo rucher node cadre rm web              # unmanage, keep the user and data
sudo rucher node cadre rm web --purge      # also delete the user + home + data
```

### `rucher node cadre recipient <name>`

Print a compartment's stored age recipient (used to encrypt its `secrets.sops.yaml`). See
[secrets.md](secrets.md).

```bash
sudo rucher node cadre recipient web    # -> age1...
```

### `rucher node key init` / `rucher node key show`

Manage the node's own age key (born on the node at `/etc/rucher/node/identity.txt`,
mode 0600; the private key never leaves the node). `init` creates it on first use and prints
its recipient; `show` prints the recipient of the existing key. Used by the GitOps
agent to unseal compartment identities. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher node key init         # -> age1... (this node's recipient)
sudo rucher node key show
```

### `rucher node agent run [--config PATH]` / `rucher node agent install [--config PATH]`

`run` performs one pull-based reconcile pass from the store described by the agent config
(default `/etc/rucher/agent.yml`); `install` writes a systemd oneshot service + timer
that run `node agent run` periodically and enables the timer. Prints
`revision <rev>: applied=<n> removed=<n>`. `--config`, when given, must come immediately
after `run`/`install`. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher node agent run --config /etc/rucher/agent.yml
sudo rucher node agent install
```

## ops

Runs from the operator machine (any OS). `plan` is a read-only dry run; `key seal` seals a
compartment identity to node(s); `nodes` reaches every node over SSH.

### `rucher ops plan [--dir DIR] [name...]`

Dry run. For each selected compartment, load and validate it and print what `apply` would do
(against an empty prior state, so the full intended change is shown): units to start/restart
and files to write. Read-only — it touches nothing on the node and does not require root.

```bash
rucher ops plan --dir ./compartments web
```

### `rucher ops key seal <name> --to <node-recipient> [--to <node-recipient> ...]`

Generate a compartment keypair, seal its private identity to every listed node recipient (so
any of those nodes can unseal it), write the sealed `identity.age` into
`./compartments/<name>/`, and print the compartment's recipient. Repeated `--to` values are
de-duplicated. This is an operator-side command used when building the store. See
[gitops-agent.md](gitops-agent.md).

```bash
rucher ops key seal web --to age1nodeA... --to age1nodeB...   # -> web's recipient
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
