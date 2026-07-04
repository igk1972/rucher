# Интеграционный прогон A на Lima-ноде

Предпосылки: lima-essaim подняла ноду `lima-essaim-01`; podman-essaim поставил podman;
на ноде есть `age`, `age-keygen`, `sops`. Бинарь собран: `GOOS=linux GOARCH=arm64 go build ./cmd/...`
и скопирован на ноду, запуск под root (`sudo`).

Шаги (все на ноде):
1. `sudo pecm new demo` — создан юзер `pecm-demo`, есть identity, напечатан recipient.
2. На Mac: `printf 'db_password: s3cr3t\n' | sops --encrypt --age <recipient> /dev/stdin > compartments/demo/secrets.sops.yaml`.
   Положить `compartments/demo/nginx.container`, `nginx.conf`, `compartment.yml (secrets.from)`.
3. `sudo pecm apply demo` → `systemctl --user -M pecm-demo@ status` показывает active; `podman secret ls` содержит `db_password`.
4. Поменять `nginx.conf`, `sudo pecm apply demo` → рестартнулся только nginx-юнит (проверить по `journalctl --user`).
5. Перешифровать секрет, `apply` → секрет пересоздан, потребитель рестартнут.
6. `sudo pecm apply demo` ещё раз → «ноль изменений» (идемпотентность).
7. `sudo pecm rm demo --purge` → юзер и данные удалены.
