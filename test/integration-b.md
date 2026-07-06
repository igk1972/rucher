# Integration run B on Lima nodes

Validates the GitOps flow: node key → seal → git store → `agent run` → apply, and removal via
`placement.yml`. **Validated on `lima-essaim-01`** (single-node: the node acts as both
operator and worker, with the store in a local git repository — no SSH/network).

Prerequisites: node is up; `podman`/`age`/`sops`/`git`/`uidmap` installed; `pecm` built
for linux/arm64 and at `/usr/local/bin/pecm`. The store is a git repository reachable by the node: a local
path (as here), a shared mount, or a remote URL. **For an SSH URL** go-git by default verifies the
host against `~/.ssh/known_hosts` — it must be pre-populated, otherwise the first clone will fail.

1. `sudo pecm node init` → node recipient `$NODE_R` (the private key is born on the node,
   `/etc/podman-essaim/node/identity.txt`, 0600).
2. Operator — in the store checkout (here `/root/fleet`, `git init -b master`):
   - `pecm keygen web --to $NODE_R` → writes `compartments/web/identity.age` (the compartment's
     identity, sealed to the node's recipient), prints the compartment's recipient `$WEB_R`;
   - `printf 'db_password: s3cr3t\n' | sops --encrypt --input-type yaml --output-type yaml --age $WEB_R /dev/stdin > compartments/web/secrets.sops.yaml`
     (**`--input-type yaml` is mandatory** — otherwise sops treats the input as binary and wraps everything in
     a single `data` key);
   - place `compartments/web/compartment.yml` (`name: web`, `secrets.from: secrets.sops.yaml`),
     `web.container` (`Secret=db_password,type=env,target=DB_PASSWORD`, `EnvironmentFile=…/app.env`),
     `app.env`;
   - `placement.yml`: `placements:\n  web: lima-essaim-01`;
   - `git add -A && git commit`.
3. `/etc/podman-essaim/agent.yml` (`node: lima-essaim-01`, `store: {kind: git, url: /root/fleet,
   branch: master}`), then `sudo pecm agent run`.
   → exit 0, `applied=1`; `web.service` active; container `systemd-web` Up; `DB_PASSWORD=s3cr3t`,
   `GREETING` from `app.env`.
4. **Check permissions**: `stat -c %a /var/lib/podman-essaim/compartments/web/.config/podman-essaim-compartment-manager/age/identity.txt`
   → `600` (the unsealed compartment private key).
5. `cat /var/lib/podman-essaim/agent-status.json` → `revision`, `applied=[{web, ok:true}]`.
6. Idempotency: a repeated `agent run` → same `InvocationID` on `web.service` (no restart).
7. Removal: `placement.yml` → `web: lima-essaim-02`, commit, `sudo pecm agent run` → `removed=[web]`,
   units/container/state removed, user `pecm-web` retained (rm without purge). `pecm rm web --purge`
   removes the user.
8. `sudo pecm agent install` → `podman-essaim-agent.timer` active
   (`systemctl status podman-essaim-agent.timer`); period = `interval` from `agent.yml`.

Multi-node (groundwork): node B with its own `node init` won't be able to unseal `identity.age`
sealed to node A's recipient (separate node keys; see the wrong-identity test in `internal/age`).
To run the same compartment on B, the operator does `keygen web --to $B_R` and commits
`identity.<B>.age` (agent selection of `identity.<node>.age` — in §14 of the spec, for now generic `identity.age`).
