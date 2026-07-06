# Integration run A on a Lima node

Prerequisites: lima-essaim has brought up node `lima-essaim-01`; podman-essaim installed podman;
the node has `age`, `age-keygen`, `sops`. The binary is built: `GOOS=linux GOARCH=arm64 go build ./cmd/...`
and copied to the node, run as root (`sudo`).

Steps (all on the node):
1. `sudo pecm new demo` — user `pecm-demo` created, identity present, recipient printed.
2. On the Mac: `printf 'db_password: s3cr3t\n' | sops --encrypt --age <recipient> /dev/stdin > compartments/demo/secrets.sops.yaml`.
   Place `compartments/demo/nginx.container`, `nginx.conf`, `compartment.yml (secrets.from)`.
3. `sudo pecm apply demo` → `systemctl --user -M pecm-demo@ status` shows active; `podman secret ls` contains `db_password`.
4. Change `nginx.conf`, `sudo pecm apply demo` → only the nginx unit restarted (verify via `journalctl --user`).
5. Re-encrypt the secret, `apply` → secret recreated, consumer restarted.
6. `sudo pecm apply demo` once more → "zero changes" (idempotency).
7. `sudo pecm rm demo --purge` → user and data removed.
