# Интеграционный прогон B на Lima-нодах

Предпосылки: lima-essaim-01/02 подняты; podman/age/sops установлены; бинарь `pecm` собран
под linux/arm64 и на нодах в `/usr/local/bin/pecm`; на Mac есть локальный git-стор (bare или
рабочий репозиторий), доступный нодам (по SSH-URL или через shared-mount).

1. На узле A: `sudo pecm node init` → recipient ноды `A_R`. То же на узле B → `B_R`.
2. На Mac (оператор), в чекауте стора:
   - `pecm keygen web --to $A_R` → пишет `compartments/web/identity.age`, печатает recipient `web_R`;
   - `printf 'db_password: s3cr3t\n' | sops --encrypt --age $web_R /dev/stdin > compartments/web/secrets.sops.yaml`;
   - положить `compartments/web/compartment.yml` (`name: web`, `secrets.from: secrets.sops.yaml`),
     `web.container` (Secret=db_password), `app.env`;
   - `placement.yml`: `placements: {web: lima-essaim-01}`;
   - commit + push.
3. На узле A: положить `/etc/podman-essaim/agent.yml` (store.url/branch), `sudo pecm agent run`.
   → `systemctl --user -M pecm-web@ status web.service` active; `DB_PASSWORD` в контейнере.
4. Проверить статус: `cat /var/lib/podman-essaim/agent-status.json` (revision, applied=[web ok]).
5. Сменить `placement.yml` на `{web: lima-essaim-02}`, push. На узле A: `sudo pecm agent run`
   → web остановлен (removed=[web]); на узле B: `keygen web --to $B_R` + re-seal, push, `agent run`
   → web поднялся на B (доказывает переносимость + раздельные node-ключи).
6. `sudo pecm agent install` на узле A → таймер поднят (`systemctl status podman-essaim-agent.timer`).
