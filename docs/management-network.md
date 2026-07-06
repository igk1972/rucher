# Management network (operator plane)

The operator plane lets an engineer see and manage the nodes from one machine. It reaches
each node over SSH, reads the node's agent status file, and aggregates the results. It is
separate from [cadre overlays](overlays.md), which are a per-workload data plane.

## Node config directory

Each node is described by `<nodes-dir>/<node>/configuration.yml` (default nodes dir
`./nodes`, overridable with `--dir`). An optional global `<nodes-dir>/configuration.yml`
is deep-merged **under** each per-node file (maps merge key-by-key; scalars and sequences are
replaced). The schema:

```yaml
network:
  address: 100.64.0.1     # the node's reachability address (set by `rucher ops nodes join`)
connection:
  host: 203.0.113.7       # explicit SSH host
  port: 22                # default 22
  user: root              # default root
  identity: /path/to/key  # private key for SSH (optional)
```

## `rucher ops nodes join <node> --address <addr>`

Records `<node>`'s static management address into its `configuration.yml` as
`network: {address: <addr>}`, preserving other keys and comments. It updates an existing
node's config (the node directory must already exist — `ops nodes join` records an address, it does
not create a node). `--address` is required and trimmed; an empty value is rejected. `--json`
switches the success line to `{"node":…,"address":…}`.

```bash
rucher ops nodes join node-a --address 100.64.0.1
rucher ops nodes join node-a --address 100.64.0.1 --json
```

A repeated `ops nodes join` with a different address simply updates the value.

## `rucher ops nodes status [--live] [--json] [node...]`

For each node (all nodes under `--dir` when none are named), the tool:

1. loads and merges the node config;
2. resolves an SSH target (see precedence below);
3. runs `cat /var/lib/rucher/agent-status.json` over SSH and parses the agent's
   [status](gitops-agent.md) (revision, applied count, removed count, per-cadre
   errors);
4. with `--live`, additionally runs `rucher node cadre status` on the node and captures its live per-unit
   `ActiveState`/`SubState`.

Output is a table by default:

```
NODE    ADDRESS      REACHABLE  REVISION  APPLIED  REMOVED  ERRORS
node-a  100.64.0.1   yes        1a2b3c…   2        0
node-b  100.64.0.2   no                   0        0        1
errors:
  node-b: ssh dial 100.64.0.2:22: ...
```

`--json` emits the rows as a JSON array instead (an empty result is `[]`, not `null`). A node
that connects but fails records the reason under `errors:` so a transport/config failure is
distinguishable from a plain "node down"; the exit code is 1 if any node is unreachable.

```bash
rucher ops nodes status
rucher ops nodes status --live node-a
rucher ops nodes status --json > status.json
```

## Native SSH client and TOFU host keys

The operator plane uses a built-in Go SSH client (`golang.org/x/crypto/ssh`) — **no system
`ssh` binary is required**. It authenticates with the target's configured `identity` key
and/or any keys exposed by an `SSH_AUTH_SOCK` agent, runs a single remote command, and
returns stdout/stderr plus the remote exit code. A non-zero remote exit is not treated as a
transport error, so callers use `err != nil || code != 0` as "unreachable". A per-command
timeout (30s) bounds a node that connects but stalls.

Host keys are trusted **TOFU** (trust on first use), backed by a per-tool known_hosts store
at `~/.config/rucher/known_hosts` (created mode 0600, separate from `~/.ssh/known_hosts`):

- an **unknown** node is accepted and its key pinned on first contact;
- a later key **change** for a pinned node is **rejected**;
- on reconnect, negotiation is constrained to the already-pinned key type.

If a node is rebuilt with a new key on the same address, remove its line from
`~/.config/rucher/known_hosts` before reconnecting.

## Address resolution precedence

`sshresolve.Resolve` turns a node's config into an SSH target using the first source that
applies, in this order:

1. **`network.address`** (set by `ops nodes join`) — SSH on port 22, user from `connection.user`
   (default `root`), identity from `connection.identity`.
2. **A locally-generated per-node SSH config**, if one exists for the node (used for
   locally-provisioned development VMs) — the host, port, user and identity file are taken
   from it.
3. **The explicit `connection:` block** — `host`, `port` (default 22), `user` (default
   `root`), `identity`.

If none apply, resolving the node is an error.
