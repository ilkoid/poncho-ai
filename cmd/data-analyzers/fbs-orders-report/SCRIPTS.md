# Исторические скрипты (ретированы 2026-08-23)

`fetch-fbs-orders.sh` и `import-fbs-snapshot.sh` — разовая curl+jq+psql выгрузка
FBS-снапшота от 2026-08-16, наполнившая `public.fbs_orders` / `fbs_orders_status`.

Заменены штатным v2-загрузчиком `cmd/data-downloaders/download-wb-fbs-orders-v2`
(PG-only, регулярное обновление, журнал статусов, лента заказов). Скрипты оставлены
здесь как историческая справка; НЕ запускать — `import-fbs-snapshot.sh` делает
DROP TABLE.
