# GitOps agent

Instead of an operator pushing changes to each node, every node can pull its own desired
state from a shared **store** and reconcile itself. One agent pass fetches the store, works
out which compartments this node should run (from `placement.yml`), applies them, and
unmanages the rest.

## The store

The store holds the whole fleet's desired state:

```
<store root>/
  placement.yml
  compartments/
    web/
      compartment.yml
      web.container
      app.env
      secrets.sops.yaml     # encrypted to web's recipient
      identity.age          # web's identity, sealed to the node(s) that run it
    db/
      …
```

Two backends are supported (chosen by `store.kind` in the agent config):

- **git** (`kind: git`) — clone/pull a branch with an in-process git client (`go-git`), so no
  system `git` binary is required on the worker. Auth is optional:
  - `sshKey` → git-over-SSH with that private key. By default the SSH transport verifies the
    server against `~/.ssh/known_hosts` (pre-seed it, or set `insecureHostKey: true` to skip
    verification for freshly provisioned nodes).
  - `token` → HTTPS basic auth (`user` defaults to `git`; some providers want e.g.
    `oauth2`).
  - neither → anonymous / local-path / shared-mount access.
  If a pull fails transiently but a valid checkout already exists, the agent keeps running on
  the last-good revision rather than aborting reconciliation.
- **S3** (`kind: s3`) — list and download every object under a prefix from an S3-compatible
  endpoint into the local checkout. The revision is a deterministic hash over the object set
  (sorted `key<TAB>etag`). Object keys that would escape the cache directory are rejected.

The checkout is cached at `/var/lib/rucher/store`.

## `placement.yml`

Maps each compartment to the node(s) that should run it. A value may be a single node id or a
list; the node id defaults to the OS hostname (overridable in the agent config). Decoded
strictly — a typo such as `placement:` (singular) is an error rather than silently unmanaging
every compartment.

```yaml
placements:
  web: node-a
  db:
    - node-a
    - node-b
```

## Identities: node key and sealed compartment keys

- **Node key** — each node owns an age identity created by `rucher node key init` at
  `/etc/rucher/node/identity.txt` (mode 0600). The private key is born on the node and
  never leaves it; `rucher node key show` prints its public recipient.
- **Sealed compartment key** — a compartment's private age identity (which decrypts its
  `secrets.sops.yaml`) is not stored in cleartext in the store. The operator runs
  `rucher ops key seal <name> --to <node-recipient> [--to …]`, which generates the compartment
  keypair, seals the identity to each target node's recipient (age writes one stanza per
  recipient, so any of those nodes can unseal it), writes it to
  `compartments/<name>/identity.age`, and prints the compartment recipient (used to encrypt
  `secrets.sops.yaml`). Commit both files to the store.

At apply time the agent unseals `identity.age` with the node key and installs it at the
compartment's `identity.txt` path — exactly where the decrypt step reads it (see
[secrets.md](secrets.md)). A node whose key was not among the `--to` recipients cannot unseal
the identity and cannot run that compartment.

## `node agent run` — one pass

`rucher node agent run [--config PATH]` (default config `/etc/rucher/agent.yml`):

1. Sync the store into the checkout; obtain the current revision.
2. Read `placement.yml`; compute the compartments assigned to this node.
3. For each assigned compartment: ensure the `rucher-<name>` user, unseal + install its
   identity (no-op if it ships no `identity.age`), load it from the checkout, and run the
   standard `reconcile.Apply` (see [compartments.md](compartments.md)).
4. Unmanage every compartment currently managed on this node but no longer assigned
   (`rucher node cadre rm` without `--purge`: stop units, drop state, keep the user and data).
5. Write a status summary to `/var/lib/rucher/agent-status.json` and print
   `revision <rev>: applied=<n> removed=<n>`. A non-zero exit means one or more compartments
   failed to apply.

The status file is what the operator management plane reads over SSH — see
[management-network.md](management-network.md).

## Agent config (`/etc/rucher/agent.yml`)

```yaml
node: node-a               # this node's id in placement.yml (default: OS hostname)
interval: 30s              # how often the installed timer fires (default 30s)
store:
  kind: git                # "git" (default) | "s3"
  # --- git fields ---
  url: git@example.com:fleet/store.git
  branch: main             # default "main"
  sshKey: /etc/rucher/store_ed25519   # optional (git-over-ssh)
  token: ""                # optional (https basic auth)
  user: ""                 # https basic-auth username (default "git")
  insecureHostKey: false   # skip SSH host-key verification for the git remote
  # --- s3 fields (kind: s3) ---
  endpoint: s3.example.com:9000   # host:port, no scheme
  bucket: fleet
  prefix: store/           # key prefix within the bucket ("" = bucket root)
  accessKey: ""
  secretKey: ""
  region: ""
  useSSL: true
```

## `node agent install` — periodic reconcile

`rucher node agent install [--config PATH]` writes a systemd oneshot service plus a timer and enables
the timer:

- `/etc/systemd/system/rucher-agent.service` — `Type=oneshot`, running
  `/usr/local/bin/rucher node agent run --config <PATH>` (so `rucher` must be installed at
  `/usr/local/bin/rucher`);
- `/etc/systemd/system/rucher-agent.timer` — `OnBootSec=30s`,
  `OnUnitActiveSec=<interval>` (default `30s`), `WantedBy=timers.target`.

It then runs `systemctl daemon-reload` and `systemctl enable --now
rucher-agent.timer`.

## Operator workflow (sketch)

```bash
# on each node:
sudo rucher node key init                   # -> node recipient

# on the operator, in the store checkout:
rucher ops key seal web --to <node-a-recipient>   # writes compartments/web/identity.age; prints web's recipient
printf 'db_password: s3cr3t\n' \
  | sops --encrypt --input-type yaml --output-type yaml --age <web-recipient> /dev/stdin \
  > compartments/web/secrets.sops.yaml
# add compartment.yml, units, support files, and placement.yml, then commit + push

# on the node:
sudo rucher node agent run      # applied=1
sudo rucher node agent install  # reconcile on a timer
```
