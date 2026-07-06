# Integration run A on a Lima node

Prerequisites: lima-essaim has brought up node `lima-essaim-01`; rucher installed podman;
the node has `sops`. The binary is built: `GOOS=linux GOARCH=arm64 go build ./cmd/...`
and copied to the node, run as root (`sudo`).

Steps (all on the node):
1. `sudo rucher new demo` — user `rucher-demo` created, identity present, recipient printed.
2. On the Mac: `printf 'db_password: s3cr3t\n' | sops --encrypt --age <recipient> /dev/stdin > compartments/demo/secrets.sops.yaml`.
   Place `compartments/demo/nginx.container`, `nginx.conf`, `compartment.yml (secrets.from)`.
3. `sudo rucher apply demo` → `systemctl --user -M rucher-demo@ status` shows active; `podman secret ls` contains `db_password`.
4. Change `nginx.conf`, `sudo rucher apply demo` → only the nginx unit restarted (verify via `journalctl --user`).
5. Re-encrypt the secret, `apply` → secret recreated, consumer restarted.
6. `sudo rucher apply demo` once more → "zero changes" (idempotency).
7. `sudo rucher rm demo --purge` → user and data removed.
