# Интеграционный прогон B на Lima-нодах

Проверяет GitOps-поток: node-ключ → seal → git-стор → `agent run` → apply, и снятие через
`placement.yml`. **Валидировано на `lima-essaim-01`** (single-node: нода выступает и
оператором, и рабочей, со стором в локальном git-репозитории — без SSH/сети).

Предпосылки: нода поднята; `podman`/`age`/`sops`/`git`/`uidmap` установлены; `pecm` собран
под linux/arm64 и в `/usr/local/bin/pecm`. Стор — git-репозиторий, доступный ноде: локальный
путь (как здесь), shared-mount или удалённый URL. **Для SSH-URL** go-git по умолчанию сверяет
хост по `~/.ssh/known_hosts` — его нужно предзаполнить, иначе первый clone упадёт.

1. `sudo pecm node init` → recipient ноды `$NODE_R` (приватный ключ рождается на ноде,
   `/etc/podman-essaim/node/identity.txt`, 0600).
2. Оператор — в чекауте стора (здесь `/root/fleet`, `git init -b master`):
   - `pecm keygen web --to $NODE_R` → пишет `compartments/web/identity.age` (identity
     compartment'а, запечатанный на recipient ноды), печатает recipient compartment'а `$WEB_R`;
   - `printf 'db_password: s3cr3t\n' | sops --encrypt --input-type yaml --output-type yaml --age $WEB_R /dev/stdin > compartments/web/secrets.sops.yaml`
     (**`--input-type yaml` обязателен** — иначе sops возьмёт вход как бинарь и завернёт всё в
     один ключ `data`);
   - положить `compartments/web/compartment.yml` (`name: web`, `secrets.from: secrets.sops.yaml`),
     `web.container` (`Secret=db_password,type=env,target=DB_PASSWORD`, `EnvironmentFile=…/app.env`),
     `app.env`;
   - `placement.yml`: `placements:\n  web: lima-essaim-01`;
   - `git add -A && git commit`.
3. `/etc/podman-essaim/agent.yml` (`node: lima-essaim-01`, `store: {kind: git, url: /root/fleet,
   branch: master}`), затем `sudo pecm agent run`.
   → exit 0, `applied=1`; `web.service` active; контейнер `systemd-web` Up; `DB_PASSWORD=s3cr3t`,
   `GREETING` из `app.env`.
4. **Проверить перм**: `stat -c %a /var/lib/podman-essaim/compartments/web/.config/podman-essaim-compartment-manager/age/identity.txt`
   → `600` (распечатанный приватный ключ compartment'а).
5. `cat /var/lib/podman-essaim/agent-status.json` → `revision`, `applied=[{web, ok:true}]`.
6. Идемпотентность: повторный `agent run` → тот же `InvocationID` у `web.service` (без рестарта).
7. Снятие: `placement.yml` → `web: lima-essaim-02`, commit, `sudo pecm agent run` → `removed=[web]`,
   юниты/контейнер/state убраны, юзер `pecm-web` сохранён (rm без purge). `pecm rm web --purge`
   удаляет юзера.
8. `sudo pecm agent install` → `podman-essaim-agent.timer` активен
   (`systemctl status podman-essaim-agent.timer`); период = `interval` из `agent.yml`.

Мульти-нода (задел): узел B со своим `node init` не сможет распечатать `identity.age`,
запечатанный на recipient узла A (раздельные node-ключи; см. wrong-identity тест в `internal/age`).
Для запуска того же compartment'а на B оператор делает `keygen web --to $B_R` и коммитит
`identity.<B>.age` (выбор `identity.<node>.age` агентом — в §14 спека, пока generic `identity.age`).
