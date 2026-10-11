#!/bin/bash
# smoke-pg-fixes.sh — мини-конвейер «пощупать» новые PG-механизмы (аудит 11.10)
# на ТЕСТОВОЙ базе. Аналог download-all.sh, но только фазы, задействованные
# фиксом 4a4c92c:
#
#   Phase 1  RAW-источники агрегата региональных продаж:
#            orders (регион по srid) → opsales (деньги) → fbs-orders (лента
#            order_feed: город/ФО/статусы; лента идёт ~1 страница/мин — это
#            самая долгая фаза)
#   Phase 2  whremains — свежий снапшот остатков с row_kind (классификация
#            псевдострок «Всего/В пути» на живой записи)
#   Phase 3  build-region-sales-daily → analytical.region_sales_daily
#   Phase 4  контрольные SELECT'ы: покрытие srid-сшивки, топ-регионы,
#            row_kind-распределение, наивный SUM vs SUM(warehouse)
#
# База по умолчанию — wb_data_test (ТЕСТ!). Прод не трогаем: продовые
# конфиги переопределяются флагом --pg-database у каждого загрузчика.
#
# Промо-часть фикса (бюджет v2, bid_kopecks, camelCase list) — спящий путь
# promotion; при желании копируется отсюда заменой утилиты на
# download-wb-promotion-v2 --mock --dry-run (без API и записи).
#
# Usage:
#   bash smoke-pg-fixes.sh [--db wb_data_test] [--days 7] \
#       [--skip-downloads] [--no-whremains] [--dry-run]
#
#   --days 3       быстрее: лента/opsales/агрегат за 3 суток (~5-8 мин)
#   --skip-downloads  RAW уже залит — только агрегат и проверки
#
# Время при --days 7: ~15-25 мин (доминирует лента: 1 стр/мин, лимит API).
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
LOGDIR="$PONCHO/logs"; mkdir -p "$LOGDIR"
LOG="$LOGDIR/smoke-pg-fixes-$(date +%Y-%m-%d-%H%M).log"

DB="wb_data_test"
DAYS=7
SKIP_DOWNLOADS=0
NO_WHREMAINS=0
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) DB="$2"; shift 2 ;;
    --days) DAYS="$2"; shift 2 ;;
    --skip-downloads) SKIP_DOWNLOADS=1; shift ;;
    --no-whremains) NO_WHREMAINS=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    *) echo "Неизвестный флаг: $1 (см. шапку скрипта)"; exit 2 ;;
  esac
done

ENV_FILE="$PONCHO/.env"
if [[ -f "$ENV_FILE" ]]; then set -a; source "$ENV_FILE"; set +a; fi
: "${PGHOST:?нет PGHOST — загрузите .env}"
: "${PG_PWD:?нет PG_PWD — загрузите .env}"

run() {
  echo "── $(date '+%H:%M:%S') $* "
  "$@"
}

psql_db() {
  PGPASSWORD="$PG_PWD" psql -v ON_ERROR_STOP=1 -h "$PGHOST" -p "${PGPORT:-5432}" \
    -U "${PGUSER:-postgres}" -d "$DB" "$@"
}

{
echo "══════════════════════════════════════════════════════════════"
echo "  SMOKE PG FIXES (4a4c92c) · БД: $DB · окно: $DAYS сут"
echo "  $(date '+%Y-%m-%d %H:%M:%S') · лог: $LOG"
echo "══════════════════════════════════════════════════════════════"

if [[ "$DRY_RUN" == "1" ]]; then
  echo "--dry-run: план без запуска:"
  echo "  1. orders      → public.orders (регион по srid)"
  echo "  2. opsales     → public.operational_sales (деньги for_pay)"
  echo "  3. fbs-orders  → public.order_feed (лента: город/ФО/статусы, --days $DAYS --no-supplies)"
  [[ "$NO_WHREMAINS" == "1" ]] || echo "  4. whremains   → public.warehouse_remains (row_kind, дата сегодня)"
  echo "  5. build-region-sales-daily --days $DAYS"
  echo "  6. контрольные SELECT"
  exit 0
fi

if [[ "$SKIP_DOWNLOADS" == "0" ]]; then
  echo ""
  echo "── Phase 1: RAW-источники агрегата ──"
  run go run "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" \
    --config "$C/download-wb-orders-PG.yaml" --backend postgres --pg-database "$DB"
  run go run "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" \
    --config "$C/download-wb-opsales-PG.yaml" --backend postgres --pg-database "$DB"
  run go run "$PONCHO/cmd/data-downloaders/download-wb-fbs-orders-v2" \
    --config "$C/download-wb-fbs-orders-PG.yaml" --backend postgres --pg-database "$DB" \
    --days "$DAYS" --feed-days "$DAYS" --no-supplies

  if [[ "$NO_WHREMAINS" == "0" ]]; then
    echo ""
    echo "── Phase 2: whremains (row_kind живьём) ──"
    run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" \
      --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres \
      --pg-database "$DB" --date "$(date +%Y-%m-%d)"
  fi
else
  echo "── Phase 1-2: --skip-downloads, RAW не трогаю ──"
fi

echo ""
echo "── Phase 3: analytical.region_sales_daily ──"
run go run "$PONCHO/cmd/data-analyzers/build-region-sales-daily" \
  --config "$C/region-sales-daily-PG.yaml" --db "$DB" --days "$DAYS"

echo ""
echo "── Phase 4: контрольные проверки ──"

echo "── лента order_feed: покрытие окна и модели складов"
psql_db -c "
SELECT min(updated_at AT TIME ZONE 'Europe/Moscow')::date AS feed_from,
       max(updated_at AT TIME ZONE 'Europe/Moscow')::date AS feed_to,
       count(*) AS rows, count(*) FILTER (WHERE is_mp) AS fbs_rows
FROM public.order_feed;"

echo "── srid-сшивка: сколько юнитов ленты нашли регион (orders) и деньги (opsales)"
psql_db -c "
SELECT count(*) AS feed_units,
       count(*) FILTER (WHERE o.srid IS NOT NULL) AS with_region_pct_base,
       round(100.0 * count(*) FILTER (WHERE o.srid IS NOT NULL) / count(*), 1) AS region_cover_pct,
       round(100.0 * count(*) FILTER (WHERE s.srid IS NOT NULL) / count(*), 1) AS money_cover_pct
FROM public.order_feed f
LEFT JOIN (SELECT DISTINCT srid FROM public.orders)  o USING (srid)
LEFT JOIN (SELECT DISTINCT srid FROM public.operational_sales) s USING (srid);"

echo "── агрегат: строки по дням и выкупы"
psql_db -c "
SELECT stat_day, count(*) AS cells, sum(events_qty) AS events,
       sum(buyout_qty) AS buyouts, sum(return_qty) AS returns,
       sum(for_pay_sales_sum - for_pay_returns_sum)::bigint AS net_pay
FROM analytical.region_sales_daily GROUP BY stat_day ORDER BY stat_day;"

echo "── топ-5 регионов по выкупам (окно)"
psql_db -c "
SELECT region_name, sum(buyout_qty) AS buyouts,
       sum(for_pay_sales_sum - for_pay_returns_sum)::bigint AS net_pay
FROM analytical.region_sales_daily
WHERE region_name <> '' GROUP BY region_name
ORDER BY buyouts DESC LIMIT 5;"

if [[ "$NO_WHREMAINS" == "0" ]] && [[ "$SKIP_DOWNLOADS" == "0" ]]; then
  echo "── whremains: распределение row_kind свежего снапшота"
  psql_db -c "
SELECT row_kind, count(*) AS rows, sum(quantity) AS qty
FROM public.warehouse_remains
WHERE snapshot_date = (SELECT max(snapshot_date) FROM public.warehouse_remains)
GROUP BY row_kind ORDER BY row_kind;"

  echo "── наивный SUM vs SUM(row_kind='warehouse') на топ-5 nm (двойной счёт)"
  psql_db -c "
WITH topnm AS (
  SELECT nm_id FROM public.warehouse_remains WHERE row_kind='warehouse'
  GROUP BY nm_id ORDER BY count(*) DESC LIMIT 5)
SELECT w.nm_id,
       sum(w.quantity) AS naive_sum,
       sum(w.quantity) FILTER (WHERE w.row_kind='warehouse') AS wh_sum,
       round(sum(w.quantity)::numeric /
             NULLIF(sum(w.quantity) FILTER (WHERE w.row_kind='warehouse'), 0), 2) AS inflation_x
FROM public.warehouse_remains w
WHERE w.nm_id IN (SELECT nm_id FROM topnm)
  AND w.snapshot_date = (SELECT max(snapshot_date) FROM public.warehouse_remains)
GROUP BY w.nm_id ORDER BY w.nm_id;"
fi

echo ""
echo "════ Готово: $(date '+%H:%M:%S'). Агрегат читается также напрямую:"
echo "     SELECT * FROM analytical.region_sales_daily ORDER BY stat_day DESC LIMIT 20;"
echo "════"
} 2>&1 | tee "$LOG"
