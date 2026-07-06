# Пример: compartment overlay (L3-mesh через tailscale-сайдкар)

Готовый compartment, который даёт своим рабочим нагрузкам прозрачную L3-связность в тайнете
между хостами — без изменения кода менеджера. Это обычные «непрозрачные» квадлеты: менеджер
раскладывает их как есть, а authkey едет через штатный механизм секретов (`secrets.create`
-> podman-секрет -> env сайдкара).

## Что внутри

| Файл | Роль |
|------|------|
| `compartment.yml` | манифест; `secrets.create: [ts-authkey]` делает authkey podman-секретом |
| `overlay-demo.pod` | под, общий netns для сайдкара и нагрузки |
| `overlay-ts.container` | tailscale-сайдкар в **kernel-режиме** (`/dev/net/tun`, `NET_ADMIN`/`NET_RAW`, `TS_USERSPACE=false`) — поднимает `tailscale0` с адресом `100.x` |
| `overlay-app.container` | реальная нагрузка (nginx); **без** device и capability — прозрачно ходит в тайнет через netns пода |
| `secrets.sops.example.yaml` | PLAINTEXT-шаблон authkey; зашифруй в `secrets.sops.yaml`, реальный ключ не коммить |

Привилегия заперта в сайдкаре: только `overlay-ts` держит `/dev/net/tun` и capability,
`overlay-app` — обычный непривилегированный контейнер, но пользуется тем же `tailscale0`,
потому что делит netns пода.

## Как применить

Файлы compartment'а лежат в подкаталоге `overlay-demo/`. `apply` берёт **родительский**
каталог (`--dir`), а имя выбирает подкаталог — то есть `--dir` указывает на этот каталог
(`test/overlay-example/`), а не на сам `overlay-demo/`. Команды ниже — из этого каталога:

```bash
# 1. authkey из админки tailscale -> зашифровать на recipient этого compartment'а
#    (в overlay-demo/secrets.sops.yaml, рядом с compartment.yml):
pecm age recipient overlay-demo                     # -> age1...
printf 'ts-authkey: tskey-auth-XXXX\n' \
  | sops --encrypt --input-type yaml --output-type yaml --age <recipient> /dev/stdin \
  > overlay-demo/secrets.sops.yaml

# 2. разложить и запустить (--dir = родитель, overlay-demo = подкаталог; или GitOps-агентом):
pecm apply --dir . overlay-demo
```

Хост должен иметь загруженный модуль `tun` и доступный `/dev/net/tun` для пользователя
compartment'а — это задача слоя провижининга (см. runbook).

Подробный разбор и что именно проверено — `test/integration-overlay.md`.
