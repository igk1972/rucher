# Quick start

From nothing to a running cadre in about five minutes. This walks a **single node**
with no secrets — the smallest useful path. For the full model see
[cadres.md](cadres.md); for many nodes and GitOps see [gitops-agent.md](gitops-agent.md).

`rucher` runs **as root on a Linux node** (Debian arm64/amd64) — it creates users and
drives their rootless podman under systemd. If you're on a Mac or just trying it out, run
these steps inside a Linux VM; a [Lima](https://lima-vm.io) VM works well (it's what the
integration tests drive).

## 1. Install the binary on the node

Download the release binary for the node's architecture:

```bash
arch=$(dpkg --print-architecture)   # arm64 or amd64
curl -fsSLo rucher \
  "https://github.com/igk1972/rucher/releases/latest/download/rucher_linux_${arch}"
chmod +x rucher
sudo mv rucher /usr/local/bin/rucher
```

Or build it yourself — see [Build](../README.md#build) in the README.

Check the node has what a cadre needs (podman rootless-capable, `uidmap`, systemd):

```bash
rucher --help
podman --version && command -v newuidmap
```

## 2. Author a cadre

A cadre is just a directory. Create one with a manifest and a single Quadlet unit:

```bash
mkdir -p cadres/hello
```

`cadres/hello/rucher.yml` — the cadre's name is its directory name, so with no secrets
or registries the manifest can stay effectively empty:

```yaml
# hello cadre: no secrets, no registries. The unit below runs a public image.
```

`cadres/hello/web.container` — a minimal Quadlet unit:

```ini
[Unit]
Description=hello web

[Container]
Image=docker.io/library/nginx:alpine
PublishPort=8080:80

[Install]
WantedBy=default.target
```

## 3. Create the cadre user

This provisions the dedicated `rucher-hello` system user and its age identity (used later
if you add secrets), and prints the cadre's age recipient:

```bash
sudo rucher node cadre new hello
```

## 4. Preview, then apply

Dry-run first to see exactly what will change:

```bash
rucher ops plan --dir ./cadres hello
```

Then reconcile the cadre onto the node — this lays the unit into the cadre user's
`~/.config/containers/systemd/`, runs `systemctl --user daemon-reload`, and starts the
service:

```bash
sudo rucher node cadre apply --dir ./cadres hello
```

`apply` is idempotent: run it again and it starts/restarts nothing.

## 5. Verify

```bash
sudo rucher node cadre status hello        # per-unit ActiveState/SubState
curl -s localhost:8080 | head -n1          # -> nginx welcome page
sudo rucher node cadre logs hello web      # last journal lines for the unit
```

## 6. Clean up

```bash
sudo rucher node cadre rm hello --purge    # stop, unmanage, and delete the user + data
```

## Next steps

- **Add a secret** — encrypt a value to the cadre's recipient and let podman mount it:
  see the [secret workflow](../README.md#secret-workflow) and [secrets.md](secrets.md).
- **More than one node** — the pull-based GitOps agent reconciles each node from a git/S3
  store; see [gitops-agent.md](gitops-agent.md).
- **Manage nodes over SSH** — `ops nodes join` / `ops nodes status` /
  `ops nodes deploy`; see [management-network.md](management-network.md).
- **Cross-node networking** — [overlays.md](overlays.md) and the ready
  [overlay example](examples/overlay-example/).
