# GitOps agent

Instead of an operator pushing changes to each node, every node can pull its own desired
state from a shared **store** and reconcile itself. One agent pass fetches the store, works
out which cadres this node should run (from `placement.yml`), applies them, and
unmanages the rest.

## The store

The store holds all nodes' desired state:

```
<store root>/
  placement.yml
  cadres/
    web/
      rucher.yml
      web.container
      app.env
      secrets.sops.yaml     # encrypted to web's recipient
      identity.age          # web's identity, sealed to the node(s) that run it
    db/
      …
```

Two backends are supported (chosen by `store.kind` in the agent config):

- **git** (`kind: git`) — clone/pull a branch with an in-process git client (`go-git`). Auth is optional:
  - `sshKey` → git-over-SSH with that private key. By default the SSH transport verifies the
    server against `~/.ssh/known_hosts` (pre-seed it, or set `insecureHostKey: true` to skip
    verification for freshly provisioned nodes).
  - `token` → HTTPS basic auth (`user` defaults to `git`; some providers want e.g.
    `oauth2`).
  - neither → anonymous / local-path / shared-mount access.
  If a pull fails transiently but a valid checkout already exists, the agent keeps running on
  the last-good revision rather than aborting reconciliation.
- **S3** (`kind: s3`) — list the objects under a prefix on an S3-compatible endpoint and
  download only those whose ETag changed (new or modified) into the local checkout, dropping
  ones that disappeared — an incremental sync, not a full re-download each pass. The revision
  is a deterministic hash over the object set (sorted `key<TAB>etag`). Object keys that would
  escape the cache directory are rejected.

The checkout is cached at `/var/lib/rucher/store`. For **git**, if `store.url` or `branch`
changes, the agent detects that the cache no longer matches the configured remote and re-clones
it fresh. For **S3**, the ETags of the cached objects are tracked in a sidecar state file, and a
change of endpoint/bucket/prefix triggers a clean re-download (no need to delete the cache by
hand in either case).

## `placement.yml`

Maps each cadre to the node(s) that should run it. A value may be a single node id or a
list; the node id defaults to the OS hostname (overridable in the agent config). Decoded
strictly — a typo such as `placement:` (singular) is an error rather than silently unmanaging
every cadre.

```yaml
placements:
  web: node-a
  db:
    - node-a
    - node-b
```

## Identities: node key and sealed cadre keys

- **Node key** — each node owns an age identity created by `rucher node key init` at
  `/etc/rucher/node/identity.txt` (mode 0600). The private key is born on the node and
  never leaves it; `rucher node key show` prints its public recipient.
- **Sealed cadre key** — a cadre's private age identity (which decrypts its
  `secrets.sops.yaml`) is not stored in cleartext in the store. The operator runs
  `rucher ops key seal <name> --to <node-recipient> [--to …]`, which generates the cadre
  keypair, seals the identity to each target node's recipient (age writes one stanza per
  recipient, so any of those nodes can unseal it), writes it to
  `cadres/<name>/identity.age`, and prints the cadre recipient (used to encrypt
  `secrets.sops.yaml`). Commit both files to the store.

At apply time the agent unseals `identity.age` with the node key and installs it at the
cadre's `identity.txt` path — exactly where the decrypt step reads it (see
[secrets.md](secrets.md)). A node whose key was not among the `--to` recipients cannot unseal
the identity and cannot run that cadre.

## `node agent run` — one pass

`rucher node agent run [--config PATH]` (default config `/etc/rucher/agent.yml`):

1. Sync the store into the checkout; obtain the current revision.
2. Read `placement.yml`; compute the cadres assigned to this node.
3. For each assigned cadre: ensure the `rucher-<name>` user, unseal + install its
   identity (no-op if it ships no `identity.age`), load it from the checkout, and run the
   standard `reconcile.Apply` (see [cadres.md](cadres.md)).
4. Unmanage every cadre currently managed on this node but no longer assigned
   (`rucher node cadre rm` without `--purge`: stop units, drop state, keep the user and data).
5. Write a status summary to `/var/lib/rucher/agent-status.json` and print
   `revision <rev>: applied=<n> removed=<n>`. A non-zero exit means one or more cadres
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
  url: git@example.com:infrastructure/store.git
  branch: main             # default "main"
  sshKey: /etc/rucher/store_ed25519   # optional (git-over-ssh)
  token: ""                # optional (https basic auth)
  user: ""                 # https basic-auth username (default "git")
  insecureHostKey: false   # skip SSH host-key verification for the git remote
  # --- s3 fields (kind: s3) ---
  endpoint: s3.example.com:9000   # host:port, no scheme
  bucket: infrastructure
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
# on the operator — provision + install rucher + bootstrap the agent on the node,
# printing node-a's recipient (nodes are configured under ./nodes):
rucher ops nodes deploy --store-url git@example.com:store.git node-a

# in the store checkout — one command generates the cadre identity, seals it to the node,
# and encrypts the secrets (edit web.plain.yaml first) into cadres/web/:
rucher ops secrets encrypt --cadre web --seal-to <node-a-recipient> --in web.plain.yaml
# add rucher.yml, units, support files, and placement.yml, then commit + push —
# the agent installed by deploy picks it up on its timer.
```
