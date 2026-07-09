# Contributing to rucher

Thanks for your interest in contributing.

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). By
participating, you are expected to uphold it. Report unacceptable behavior to
igk@igk.one.

## License of contributions

rucher is licensed under **AGPL-3.0-or-later**. By submitting a contribution,
you agree that it is provided under the same license (inbound = outbound).

## Developer Certificate of Origin (DCO)

We use the Developer Certificate of Origin to track provenance of contributions.
Every commit must be signed off, certifying that you wrote the code (or otherwise
have the right to submit it under the project's license). This is a lightweight,
CLA-free alternative and requires no separate paperwork.

### How to sign off

Add a `Signed-off-by` line to each commit using your real name and email:

```
git commit -s -m "your message"
```

This appends a trailer like:

```
Signed-off-by: Jane Doe <jane@example.com>
```

Your `user.name` and `user.email` git config must match a real identity.
To sign off an existing commit, amend it: `git commit --amend -s`.
To sign off a whole branch: `git rebase --signoff main`.

### The certificate

By signing off, you certify the following:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

## Before you open a PR

- Keep commits focused; sign off each one.
- Run `go test ./...`; if the change touches on-node behavior, run the
  integration suite (see below).
- New source files should carry the SPDX header:
  `// SPDX-License-Identifier: AGPL-3.0-or-later`.

## Running the integration tests (Lima)

The end-to-end suite drives **real Lima VMs** and is gated behind the `integration`
build tag, so plain `go test ./...` never touches a node. You need `limactl`, `go`,
`git`, and `sops` on the host (plus `openssl`/`gh` for the headscale overlay test and
`rclone` for the S3 store test, which skips if absent).

Bring up the node swarm (idempotent; creates the VMs and installs podman + `uidmap` +
`/dev/net/tun`), then run the suite:

```bash
go run ./test/integration/cmd/setup-nodes    # create + provision + verify the Lima nodes
go test -tags integration ./test/integration/ -v
```

The tests **do not provision anything** — they fail (never skip) if a node they need is
not `Running`. Full details, per-test coverage, and caveats live in
[`test/integration/README.md`](test/integration/README.md).

