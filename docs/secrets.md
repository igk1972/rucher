# Secrets (SOPS + age)

Secrets live **encrypted at rest** inside a cadre directory, safe to commit to the
store. The model is [SOPS](https://github.com/getsops/sops) for the file format plus
[age](https://age-encryption.org/) (X25519) for the encryption backend. Plaintext is only
ever held in the manager's memory and fed to podman over stdin — never written to disk,
never passed on a command line.

## Per-cadre identity

Every cadre has its own age identity, generated **in-process** (via `filippo.io/age`)
the first time the cadre's user is provisioned. It is stored under the cadre
user's home:

```
<home>/.config/rucher/age/
  identity.txt     # the private age identity (mode 0600)
  recipient.txt    # the public recipient (age1...)
```

Retrieve the recipient with `rucher node cadre recipient <name>` (or it is printed by `rucher node cadre new`).

The per-cadre identity scopes **at-rest** access in the store: a cadre's file is
encrypted only to that cadre's recipient, so cadres cannot read each other's
secrets from the store. It does not scope runtime access on the node — root (the manager)
can already read every cadre's identity and every secret.

## Encrypting a cadre's secrets

Encrypt a plaintext key/value document to the cadre's recipient and save it as the
cadre's SOPS file (default filename `secrets.sops.yaml`):

```bash
sudo rucher node cadre new web                         # prints the cadre recipient
REC=$(sudo rucher node cadre recipient web)

printf 'db_password: s3cr3t\n' \
  | sops --encrypt --input-type yaml --output-type yaml --age "$REC" /dev/stdin \
  > cadres/web/secrets.sops.yaml
```

`--input-type yaml` matters: without it SOPS may treat the input as binary and wrap the whole
document under a single `data` key, so the individual keys would not be addressable.

The manifest points at this file and (optionally) narrows which keys become podman secrets:

```yaml
# rucher.yml
name: web
secrets:
  from: secrets.sops.yaml
  create: [db_password]     # only this key -> a podman secret; omit `create` to take all keys
```

## Decryption at `apply`

When a cadre ships a SOPS file, `apply` decrypts it as **root**:

```
env SOPS_AGE_KEY_FILE=<home>/.../age/identity.txt \
    sops -d --output-type json <secrets.sops.yaml>
```

Root can read both the (root-owned) SOPS file and the cadre user's age identity. The
decrypted JSON is parsed into an in-memory key/value map. From there:

- keys selected by `secrets.create` (or all keys, if `create` is omitted) are turned into
  podman secrets via `podman secret create <key> -`, the value piped over stdin, run as the
  cadre user. A changed value hash re-creates the secret; a key removed from the file
  removes the secret (see [cadres.md](cadres.md) for the diff rules).
- each `registries.login[]` entry logs in with `podman login --username <u> --password-stdin`,
  the password taken from the decrypted key named by `passwordKey`.

Units consume the resulting podman secrets the normal Quadlet way, e.g.
`Secret=db_password,type=env,target=DB_PASSWORD`.

## Node tooling

The node needs the **`sops` binary** on `PATH` for decryption. age is **not** a separate node
dependency:

- identity **generation** is in-process (built into the manager);
- identity **decryption** uses SOPS's built-in age backend, driven by the
  `SOPS_AGE_KEY_FILE` environment variable — there is no separate age CLI to install.

See [node-requirements.md](node-requirements.md).

## Relation to the GitOps agent

Under the GitOps agent, the cadre's private identity does not live on the node until it
is needed: it is sealed to the node's recipient as `identity.age` and committed to the store.
The agent unseals it with the node key and installs it at the same `identity.txt` path before
running the decrypt+apply described above. See [gitops-agent.md](gitops-agent.md).
