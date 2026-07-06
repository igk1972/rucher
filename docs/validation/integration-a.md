# Integration run A on a Lima node

Prerequisites: lima-essaim has brought up node `lima-essaim-01`; rucher installed podman;
the node has `sops`. The binary is built: `GOOS=linux go build -trimpath -ldflags="-s -w" ./cmd/...`
and copied to the node, run as root (`sudo`).

Steps (all on the node):
1. `sudo rucher node cadre new demo` — user `rucher-demo` created, identity present, recipient printed.
2. On the Mac: `printf 'db_password: s3cr3t\n' | sops --encrypt --age <recipient> /dev/stdin > cadres/demo/secrets.sops.yaml`.
   Place `cadres/demo/nginx.container`, `nginx.conf`, `rucher.yml (secrets.from)`.
3. `sudo rucher node cadre apply demo` → `systemctl --user -M rucher-demo@ status` shows active; `podman secret ls` contains `db_password`.
4. Change `nginx.conf`, `sudo rucher node cadre apply demo` → only the nginx unit restarted (verify via `journalctl --user`).
5. Re-encrypt the secret, `apply` → secret recreated, consumer restarted.
6. `sudo rucher node cadre apply demo` once more → "zero changes" (idempotency).
7. `sudo rucher node cadre rm demo --purge` → user and data removed.
