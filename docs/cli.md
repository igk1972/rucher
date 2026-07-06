# CLI reference

The binary is `rucher`. Invocation is `rucher <command> [args]`. Compartment lifecycle commands
run as **root** on the host (they create users and drive per-user systemd). The commands:

```
new  plan  apply  status  rm  logs  age  node  agent  keygen  net  hosts
```

Unknown commands and missing required arguments print a usage line and exit non-zero.

## Shared conventions

### `--dir DIR` (plan, apply)

`--dir` is the **parent** directory whose immediate subdirectories are compartments; the
subdirectory name is the compartment name. It defaults to `./compartments`. Any positional
arguments after the flags are compartment names to act on; with none, every subdirectory is
selected. A requested name that is not a subdirectory of `--dir` is an error (this guards
against pointing `--dir` at a single compartment folder instead of its parent).

```bash
rucher apply --dir ./compartments web        # reconcile ./compartments/web
rucher apply --dir .                          # reconcile every compartment under .
```

### `--hosts DIR` (net, hosts)

`--hosts` points at the directory of per-host config folders (`<DIR>/<host>/configuration.yml`).
It defaults to `./hosts`. See [management-network.md](management-network.md).

---

## `rucher new <name>`

Provision a compartment's OS user (`rucher-<name>`) and its age identity if absent, then print
the compartment's age recipient. Idempotent: re-running returns the existing recipient.

```bash
sudo rucher new web        # -> age1... (the compartment's recipient)
```

## `rucher plan [--dir DIR] [name...]`

Dry run. For each selected compartment, load and validate it and print what `apply` would do
(against an empty prior state, so the full intended change is shown): units to start/restart
and files to write. Read-only — it touches nothing on the host and does not require root.

```bash
rucher plan --dir ./compartments web
```

## `rucher apply [--dir DIR] [name...]`

Reconcile each selected compartment onto the host: ensure the user, decrypt secrets, diff
against the last-applied state, and apply the minimal changes (write files, create secrets,
registry logins, resource limits, `daemon-reload`, start/restart/stop units). Idempotent.
Prints `started=<n> restarted=<n>` per compartment.

```bash
sudo rucher apply --dir ./compartments web
```

## `rucher status [name...]`

Print each compartment's per-unit `ActiveState`/`SubState` (via `systemctl --user show`) as a
table. With no names it reports every compartment that has a persisted state file. (Note:
`status` does not take `--dir`; it works from persisted state, not a directory.)

```bash
sudo rucher status
sudo rucher status web
```

## `rucher rm <name> [--purge]`

Unmanage a compartment: stop its units, delete their unit files (so nothing restarts on
boot), and drop the state file. The OS user, its podman secrets/volumes and the age identity
are **kept**. With `--purge` it additionally tears down the OS user and its home (terminates
the user's session and processes, `userdel -r`, removes the resource slice drop-in).

```bash
sudo rucher rm web              # unmanage, keep the user and data
sudo rucher rm web --purge      # also delete the user + home + data
```

## `rucher logs <name> <unit>`

Print the last 200 journal lines for one of a compartment's units. Read as root filtered to
the user's unit (`_SYSTEMD_USER_UNIT` + `_UID`), because a nologin system user cannot open
its own `journalctl --user`. `<unit>` is the Quadlet filename (e.g. `web.container`).

```bash
sudo rucher logs web web.container
```

## `rucher age recipient <name>`

Print a compartment's stored age recipient (used to encrypt its `secrets.sops.yaml`). See
[secrets.md](secrets.md).

```bash
sudo rucher age recipient web    # -> age1...
```

## `rucher node init` / `rucher node recipient`

Manage the node's own age key (born on the node at `/etc/rucher/node/identity.txt`,
mode 0600; the private key never leaves the node). `init` creates it on first use and prints
its recipient; `recipient` prints the recipient of the existing key. Used by the GitOps
agent to unseal compartment identities. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher node init         # -> age1... (this node's recipient)
sudo rucher node recipient
```

## `rucher agent run [--config PATH]` / `rucher agent install [--config PATH]`

`run` performs one pull-based reconcile pass from the store described by the agent config
(default `/etc/rucher/agent.yml`); `install` writes a systemd oneshot service + timer
that run `agent run` periodically and enables the timer. Prints
`revision <rev>: applied=<n> removed=<n>`. `--config`, when given, must come immediately
after `run`/`install`. See [gitops-agent.md](gitops-agent.md).

```bash
sudo rucher agent run --config /etc/rucher/agent.yml
sudo rucher agent install
```

## `rucher keygen <name> --to <node-recipient> [--to <node-recipient> ...]`

Generate a compartment keypair, seal its private identity to every listed node recipient (so
any of those nodes can unseal it), write the sealed `identity.age` into
`./compartments/<name>/`, and print the compartment's recipient. Repeated `--to` values are
de-duplicated. This is an operator-side command used when building the store. See
[gitops-agent.md](gitops-agent.md).

```bash
rucher keygen web --to age1nodeA... --to age1nodeB...   # -> web's recipient
```

## `rucher net [--hosts DIR] join <host> --address <addr> [--json]`

Record `<host>`'s static management address into `<hosts-dir>/<host>/configuration.yml` as a
`network: {address: <addr>}` block, preserving other keys and comments. The host directory
must already exist. `--address` is required and non-empty; `--json` switches the success
output to a compact JSON object. See [management-network.md](management-network.md).

```bash
rucher net join node-a --address 100.64.0.1
rucher net join node-a --address 100.64.0.1 --json
```

## `rucher hosts [--hosts DIR] status [--live] [--json] [host...]`

Gather each host's agent status over SSH and print it. Default output is a table
(`HOST ADDRESS REACHABLE REVISION APPLIED REMOVED ERRORS`) followed by an errors detail
block; `--json` emits a JSON array instead. `--live` additionally runs `rucher status` on each
reachable host and appends the live per-unit output. With no host names, every host under
`--hosts` that has a `configuration.yml` is queried. Exit code is 1 if any host is
unreachable. See [management-network.md](management-network.md).

```bash
rucher hosts status
rucher hosts status --live node-a
rucher hosts status --json
```
