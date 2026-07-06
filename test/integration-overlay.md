# Compartment overlay — прогон на Lima-нодах

Даёт рабочим нагрузкам compartment'а прозрачную L3-связность в тайнете между хостами. Форма —
обычные «непрозрачные» квадлеты: оператор пишет tailscale-сайдкар + под как всегда, authkey
едет через штатный `secrets.create`. **Код менеджера не меняется.** Пример целиком —
`test/overlay-example/`.

**Валидировано контроллером** на Lima (Debian trixie, podman 5.8.4, настоящий тайнет):
кросс-нодовая прозрачная связность из пода на lima-01 в nginx в поде на lima-02 по tailscale-IP,
без правок нагрузки и без прокси; ядро маршрутизирует `dev tailscale0`. Ниже помечено, что
проверено контроллером, а что остаётся шагом оператора.

## Чем отличается от управляющей сети C

- **Управляющая сеть C** (`pecm net join <host> --address 100.64.0.1`) — это control-plane:
  адрес самого хоста, по которому оператор/менеджер дотягивается до ноды. Пишется в
  `./hosts/<host>/configuration.yml` как `network: {address}`. Уровень — хост.
- **Compartment overlay** (этот прогон) — data-plane: членство в тайнете у конкретной
  нагрузки. Сайдкар внутри пода compartment'а даёт этому compartment'у свой адрес `100.x`.
  Уровень — рабочая нагрузка, привязано к одному compartment'у. Одно с другим не связано:
  overlay работает, даже если хосты между собой видятся вообще без сети C.

## Предпосылка на хосте (шаг провижининга, не менеджера)

- Загружен модуль ядра `tun` и `/dev/net/tun` доступен пользователю compartment'а
  (на нодах было `0666`). Проверка: `test -c /dev/net/tun && stat -c %a /dev/net/tun`.
- Это НЕ делает менеджер — место этому в слое провижининга (`podman-essaim` / образ ноды).
  Если устройства нет или прав не хватает — сайдкар с `TS_USERSPACE=false` не поднимет
  `tailscale0`.

## Почему `TS_USERSPACE=false` (критично)

Образ `docker.io/tailscale/tailscale` **по умолчанию идёт в userspace-режиме** (SOCKS5/HTTP-
прокси) — это НЕ прозрачно: нагрузке пришлось бы явно ходить через прокси. Нам нужен
kernel-режим: `TS_USERSPACE=false` + `/dev/net/tun` + `NET_ADMIN`/`NET_RAW`. Тогда сайдкар
создаёт настоящий интерфейс `tailscale0`, и ядро маршрутизирует трафик `dev tailscale0`
прозрачно — приложение не знает, что ходит через тайнет.

## Членство по compartment'ам, привилегия в сайдкаре

- Членство в тайнете — на уровень compartment'а: у каждого свой сайдкар и свой `100.x`.
- Привилегия заперта в сайдкаре. `/dev/net/tun`, `NET_ADMIN`, `NET_RAW` держит только
  `overlay-ts`. `overlay-app` — обычный непривилегированный контейнер (никаких device/cap),
  но пользуется тем же `tailscale0`, потому что делит netns пода `overlay-demo`.

## Authkey через `secrets.create`

- Ключ бери в админке tailscale (Settings -> Keys -> Auth keys; удобно reusable + pre-approved).
- Зашифруй его на age-recipient ЭТОГО compartment'а в `secrets.sops.yaml`:

  ```bash
  pecm age recipient overlay-demo                     # -> age1... recipient compartment'а
  printf 'ts-authkey: tskey-auth-XXXX\n' \
    | sops --encrypt --input-type yaml --output-type yaml --age <recipient> /dev/stdin \
    > test/overlay-example/secrets.sops.yaml
  ```

  (`--input-type yaml` обязателен — иначе sops завернёт всё в один ключ `data`; см. прогон B.)
- В `compartment.yml`: `secrets.create: [ts-authkey]` — только этот ключ становится podman-
  секретом. Сайдкар подхватывает его через `Secret=ts-authkey,type=env,target=TS_AUTHKEY`
  (podman-секрет -> env `TS_AUTHKEY`). Настоящий ключ в plaintext НЕ коммить —
  `secrets.sops.example.yaml` только образец формата.

## Применение через менеджер (шаг оператора)

Разложить и запустить как обычный compartment — правок менеджера не требуется:

```bash
# локально/прямой apply на ноде:
sudo pecm apply --dir ./test/overlay-example overlay-demo

# либо через GitOps-агента (прогон B): закоммитить compartment в стор,
# placement.yml -> overlay-demo: <нода>, затем `sudo pecm agent run`.
```

Форма квадлетов, которую применяет менеджер, проверена контроллером через `systemctl --user`:
под + сайдкар + app-юнит поднимаются, сайдкар получает адрес тайнета, authkey доставлен через
podman-секрет -> env.

## Что именно проверено (контроллер)

- Сайдкар в kernel-режиме зарегистрировался в тайнете и поднял `tailscale0` с IP `100.x`.
- Непривилегированный `overlay-app` в том же поде прозрачно пользуется `tailscale0` (без
  device, без cap).
- App в поде на lima-01 достучался до nginx в поде на lima-02 по его tailscale-IP — без правок
  приложения и без прокси; ядро маршрутизирует `dev tailscale0`.

Быстрые проверки на ноде:

```bash
# адрес сайдкара в тайнете:
podman exec overlay-ts tailscale ip -4
# маршрут наружу идёт через tailscale0 (kernel-режим, а не userspace-прокси):
podman exec overlay-app ip route get <tailscale-IP-на-другой-ноде>
# сквозная связность из нагрузки без правок приложения:
podman exec overlay-app wget -qO- http://<tailscale-IP-nginx-на-lima-02>/
```

## Очистка

```bash
sudo pecm rm overlay-demo --purge     # остановить юниты, снять с менеджмента, удалить юзера+данные
```

Нода уйдёт из тайнета сама, когда сайдкар остановлен (для ephemeral-authkey — сразу; иначе
удали её вручную в админке tailscale). `TS_STATE_DIR=/tmp/tsstate` в сайдкаре живёт внутри
контейнера, отдельного тома под состояние здесь нет.
