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
  | rucher ops secrets encrypt --to "$REC" \
  > cadres/web/secrets.sops.yaml
```

`ops secrets encrypt` reads a flat `key: value` YAML map on stdin and writes the SOPS+age
document encrypted to each `--to` recipient. It is the in-process replacement for
`sops --encrypt --age <recipient>`; the output is byte-compatible with the `sops` CLI, so
either tool can decrypt the other's files.

The manifest points at this file and (optionally) narrows which keys become podman secrets:

```yaml
# rucher.yml
secrets:
  from: secrets.sops.yaml
  create: [db_password]     # only this key -> a podman secret; omit `create` to take all non-empty keys
```

## Decryption at `apply`

When a cadre ships a SOPS file, `apply` decrypts it as **root**, **in-process**. Root can
read both the (root-owned) SOPS file and the cadre user's age identity
(`<home>/.../age/identity.txt`); the SOPS+age codec (`internal/sopsage`) unwraps the data
key with the identity, decrypts each value, verifies the MAC, and returns an in-memory
key/value map. From there:

- keys selected by `secrets.create` (or all keys with a non-empty value, if `create` is
  omitted) are turned into podman secrets via `podman secret create <key> -`, the value piped
  over stdin, run as the cadre user. A changed value hash re-creates the secret; a key removed from the file
  removes the secret (see [cadres.md](cadres.md) for the diff rules).
- each `registries.login[]` entry logs in with `podman login --username <u> --password-stdin`,
  the password taken from the decrypted key named by `passwordKey`.

Units consume the resulting podman secrets the normal Quadlet way, e.g.
`Secret=db_password,type=env,target=DB_PASSWORD`.

## Tooling

Both sides are self-contained and built into the manager:

- on the **node**, decryption is in-process (the SOPS+age codec);
- on the **operator**, encryption is in-process too — `rucher ops secrets encrypt`;
- age identities are generated in-process.

The external `sops` CLI stays interoperable within the scope below (either tool decrypts the
other's files), so it can be used interchangeably if preferred. See
[node-requirements.md](node-requirements.md).

## Compatibility scope

The built-in codec implements the subset of the SOPS format a cadre needs and is
wire-compatible with the `sops` CLI within it. The bounds:

- **Flat maps of strings.** A single level of `key: value` pairs; nested maps, sequences
  and comments are not supported.
- **`type:str` only.** Every value is encrypted as a string. sops infers `int`/`bool`/`float`
  for typed scalars, so a rucher-written file is not byte-identical to a sops-written one for
  non-string inputs — though both still decrypt either way. Cadre secrets are strings, so
  this is moot in practice.
- **Empty values** are carried as plaintext, exactly as sops does. They are the *only*
  legitimate plaintext: a **non-empty** plaintext value — e.g. a key left in the clear via
  sops's `_unencrypted` suffix — is **rejected**, because a cadre file is expected to be fully
  encrypted. Encrypt every non-empty key.
- **`mac_only_encrypted`** files use a MAC scheme this codec does not reproduce and are
  rejected with a clear error (a cadre's fully-encrypted secrets never set it).

## Relation to the GitOps agent

Under the GitOps agent, the cadre's private identity does not live on the node until it
is needed: it is sealed to the node's recipient as `identity.age` and committed to the store.
The agent unseals it with the node key and installs it at the same `identity.txt` path before
running the decrypt+apply described above. See [gitops-agent.md](gitops-agent.md).
