# Интеграционный прогон C на Lima-нодах

Предпосылки: lima-essaim-01/02 подняты; на них уже прогнан B (есть
`/var/lib/podman-essaim/agent-status.json`); `pecm` собран под linux/arm64 и на нодах;
на Mac есть `./hosts/lima-essaim-0{1,2}/configuration.yml`.

C-часть проверяется по существующему lima-SSH (overlay не нужен):

1. `pecm hosts status` (из каталога с `./hosts`) → таблица HOST/ADDRESS/REACHABLE/REVISION/
   APPLIED/REMOVED/ERRORS по обеим нодам; ревизии совпадают с тем, что применил агент.
   Недоступную ноду (останови lima-essaim-02) строка показывает REACHABLE=no, таблица не падает.
2. `pecm hosts status --live lima-essaim-01` → плюс блок `pecm status` с живыми ActiveState/SubState.
3. `pecm net join lima-essaim-01 --address 100.64.0.1` → в
   `./hosts/lima-essaim-01/configuration.yml` появляется `network: {address: 100.64.0.1}`,
   прочие ключи/комментарии на месте. Повторный `net join` с другим адресом — обновляет значение.
