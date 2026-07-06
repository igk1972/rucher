# Management network (operator plane)

The operator plane lets an engineer see and manage the fleet from one machine. It reaches
each host over SSH, reads the host's agent status file, and aggregates the results. It is
separate from [compartment overlays](overlays.md), which are a per-workload data plane.

## Host config directory

Each host is described by `<hosts-dir>/<host>/configuration.yml` (default hosts dir
`./hosts`, overridable with `--hosts`). An optional fleet-global `<hosts-dir>/configuration.yml`
is deep-merged **under** each per-host file (maps merge key-by-key; scalars and sequences are
replaced). The schema:

```yaml
network:
  address: 100.64.0.1     # the host's reachability address (set by `rucher ops ruches join`)
connection:
  host: 203.0.113.7       # explicit SSH host
  port: 22                # default 22
  user: root              # default root
  identity: /path/to/key  # private key for SSH (optional)
```

## `rucher ops ruches join <host> --address <addr>`

Records `<host>`'s static management address into its `configuration.yml` as
`network: {address: <addr>}`, preserving other keys and comments. It updates an existing
host's config (the host directory must already exist — `ops ruches join` records an address, it does
not create a host). `--address` is required and trimmed; an empty value is rejected. `--json`
switches the success line to `{"host":…,"address":…}`.

```bash
rucher ops ruches join node-a --address 100.64.0.1
rucher ops ruches join node-a --address 100.64.0.1 --json
```

A repeated `ops ruches join` with a different address simply updates the value.

## `rucher ops ruches status [--live] [--json] [host...]`

For each host (all hosts under `--hosts` when none are named), the tool:

1. loads and merges the host config;
2. resolves an SSH target (see precedence below);
3. runs `cat /var/lib/rucher/agent-status.json` over SSH and parses the agent's
   [status](gitops-agent.md) (revision, applied count, removed count, per-compartment
   errors);
4. with `--live`, additionally runs `rucher node cadre status` on the host and captures its live per-unit
   `ActiveState`/`SubState`.

Output is a table by default:

```
HOST    ADDRESS      REACHABLE  REVISION  APPLIED  REMOVED  ERRORS
node-a  100.64.0.1   yes        1a2b3c…   2        0
node-b  100.64.0.2   no                   0        0        1
errors:
  node-b: ssh dial 100.64.0.2:22: ...
```

`--json` emits the rows as a JSON array instead (an empty result is `[]`, not `null`). A host
that connects but fails records the reason under `errors:` so a transport/config failure is
distinguishable from a plain "host down"; the exit code is 1 if any host is unreachable.

```bash
rucher ops ruches status
rucher ops ruches status --live node-a
rucher ops ruches status --json > status.json
```

## Native SSH client and TOFU host keys

The operator plane uses a built-in Go SSH client (`golang.org/x/crypto/ssh`) — **no system
`ssh` binary is required**. It authenticates with the target's configured `identity` key
and/or any keys exposed by an `SSH_AUTH_SOCK` agent, runs a single remote command, and
returns stdout/stderr plus the remote exit code. A non-zero remote exit is not treated as a
transport error, so callers use `err != nil || code != 0` as "unreachable". A per-command
timeout (30s) bounds a host that connects but stalls.

Host keys are trusted **TOFU** (trust on first use), backed by a per-tool known_hosts store
at `~/.config/rucher/known_hosts` (created mode 0600, separate from `~/.ssh/known_hosts`):

- an **unknown** host is accepted and its key pinned on first contact;
- a later key **change** for a pinned host is **rejected**;
- on reconnect, negotiation is constrained to the already-pinned key type.

If a host is rebuilt with a new key on the same address, remove its line from
`~/.config/rucher/known_hosts` before reconnecting.

## Address resolution precedence

`sshresolve.Resolve` turns a host's config into an SSH target using the first source that
applies, in this order:

1. **`network.address`** (set by `ops ruches join`) — SSH on port 22, user from `connection.user`
   (default `root`), identity from `connection.identity`.
2. **A locally-generated per-host SSH config**, if one exists for the host (used for
   locally-provisioned development VMs) — the host, port, user and identity file are taken
   from it.
3. **The explicit `connection:` block** — `host`, `port` (default 22), `user` (default
   `root`), `identity`.

If none apply, resolving the host is an error.
