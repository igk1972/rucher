# rucher

Single-host manager for **podman Quadlet** workloads. It reconciles a directory of
*compartments* into running rootless-podman services under per-user systemd. Each
compartment is an isolated environment backed by a dedicated Linux system user, its own
podman secret and registry-credential store, its own age identity, and (optionally) a
systemd resource slice.

This is **sub-project A** of a larger orchestrator (multi-node placement + Tailscale
networking come in later sub-projects). A is standalone and does everything on one host.

## What it does

- You author Quadlet units (`.container`/`.volume`/`.network`/`.pod`/…) plus any support
  files (env files, configs) in a compartment directory. The tool does **not** generate
  units — you write them.
- `apply` lays the units + support files into the compartment user's
  `~/.config/containers/systemd/`, creates podman secrets, runs `systemctl --user
  daemon-reload`, and starts the services. It is **idempotent** and reconciles drift:
  changing one support file restarts only the units that use it.
- Secrets live **encrypted at rest** (SOPS + age) inside the compartment directory, safe
  to commit to a store. Plaintext is decrypted in memory and fed to podman over stdin —
  never written to disk, never passed on argv.

Native systemd gives dependencies (`After=`/`Requires=`), lifecycle hooks
(`ExecStartPre=`/…), and timers (`.timer`) for free within a compartment.

## Build

```bash
GOOS=linux GOARCH=arm64 go build -o rucher ./cmd/rucher
```

Runs as **root** on the target host (it creates users, manages linger/subuids, and drives
each user's systemd). Requires Go ≥ 1.23 to build; the only dependency is
`gopkg.in/yaml.v3`.

## Host prerequisites

Debian (arm64/amd64) with: `podman` (rootless-capable), `age`/`age-keygen`, `sops`,
`uidmap` (`newuidmap`/`newgidmap`), and systemd with `loginctl`/`runuser`. Run as root (or
via passwordless sudo).

## Compartment layout

```
compartments/<name>/
  compartment.yml          # manifest
  secrets.sops.yaml        # SOPS+age, encrypted to THIS compartment's recipient (optional)
  web.container            # your Quadlet units
  nginx.conf  app.env      # support files referenced by the units
```

```yaml
# compartment.yml
name: web                  # must equal the directory name
secrets:
  from: secrets.sops.yaml  # keys in this file become podman secrets
registries:
  login:
    - registry: ghcr.io
      username: deploy
      passwordKey: ghcr_token   # value taken from the sops file
resources:                 # optional -> systemd slice on the compartment user
  memoryMax: 512M
  cpuQuota: "50%"
```

Units reference support files by their in-place path, e.g.
`EnvironmentFile=%h/.config/containers/systemd/app.env`.

## Commands

```
rucher new <name>                     # create the user + age identity; print the recipient
rucher age recipient <name>           # print a compartment's age recipient
rucher plan [--dir DIR] [name...]     # dry-run: show what apply would change
rucher apply [--dir DIR] [name...]    # reconcile compartments onto the host
rucher status [name...]               # per-unit ActiveState/SubState
rucher logs <name> <unit>             # journalctl --user for one unit
rucher rm <name> [--purge]            # stop + unmanage; --purge also deletes the user + data
```

No `--dir` defaults to `./compartments`; no names means all compartments.

## Secret workflow

```bash
sudo rucher new web                                   # prints age1... recipient
printf 'db_password: s3cr3t\n' \
  | sops --encrypt --age <recipient> /dev/stdin \
  > compartments/web/secrets.sops.yaml              # encrypt to that recipient
sudo rucher apply --dir ./compartments web            # decrypt + create podman secret + start
```

At apply time the root agent decrypts the SOPS file using the compartment's age identity
(root can read both the file and the identity), then creates the podman secret and any
registry logins as the compartment user via stdin.

## On-host layout

| What | Path |
|------|------|
| Compartment user | `rucher-<name>` (system user, nologin) |
| Home | `/var/lib/rucher/compartments/<name>` |
| Units + support files | `<home>/.config/containers/systemd/` |
| age identity / recipient | `<home>/.config/rucher/age/` |
| Last-applied state (hashes only) | `/var/lib/rucher/compartments/state/<name>.json` |
| Resource slice drop-in | `/etc/systemd/system/user-<uid>.slice.d/50-rucher.conf` |

Each compartment user gets a unique, non-overlapping subuid/subgid block (allocated from
`/etc/subuid`), so many compartments coexist on one host.

## Host keys

`rucher hosts status` (and the rest of the operator control plane) reaches hosts with a
built-in Go SSH client — no system `ssh` binary is required. Host keys are trusted
**TOFU**: an unknown host is accepted and pinned on first contact into
`~/.config/rucher/known_hosts` (created mode 0600); a later key **change** for the
same host is rejected.

This is a separate trust store from `~/.ssh/known_hosts`, so every host re-pins on first
contact after switching to the native client. Lima nodes (previously reached via
`ssh -F ~/.lima/<name>/ssh.config`, which disables host-key checking) are now TOFU-pinned
like any other host. If a Lima VM is recreated on the **same** forwarded port with a new
key, clear its line from `~/.config/rucher/known_hosts` before reconnecting.

## Compartment overlays

Workloads can get transparent L3 mesh connectivity across hosts (a *compartment overlay*)
without any manager code change. It fits the opaque-Quadlet model: the operator authors a
kernel-mode Tailscale sidecar plus the app in one pod, and the auth key rides the existing
`secrets.create` machinery (podman secret → sidecar env). Privilege stays confined to the
sidecar; the unprivileged app shares the pod netns and reaches the tailnet transparently.
This is distinct from the operator control-plane network (`rucher net join`, which sets a
*host's* management address). See the runbook
[`test/integration-overlay.md`](test/integration-overlay.md) and the ready example in
[`test/overlay-example/`](test/overlay-example/).

## Testing

Pure logic and the shell-out layer are unit-tested with a fake command runner
(`go test ./...`). End-to-end behavior on a real host is exercised by
[`test/integration.md`](test/integration.md) on a Lima node.
