#!/bin/bash
# smoke-pg-promo.sh — живой смоук рекламного домена WB (аудит dev_wb_promo_fixes)
# на ТЕСТОВОЙ базе. Отвечает на два вопроса:
#
#   1. Ушла ли 429 на advert-api?  — считаем «status 429» в логе прогона
#      (и фатальные, и пережитые ретраями), отдельно прочие ошибки.
#   2. Работает ли зафиксированное (4a4c92c) вживую:
#      - POST /api/advert/v2/budget (v1 GET отключается 16.11.2026) —
#        свежие строки campaign_budget с currency;
#      - normquery/list camelCase — кластеры пишутся (в т.ч. archived);
#      - bid_kopecks — канон в копейках, инвариант floor: bid = bid_kopecks/100.
#
# Фазы только на advert-api.wildberries.ru (calendar/finance выключены —
# не трогаем штормовые seller-analytics хенды и не добавляем шум в замер).
# Phase 0 — пререквизит download-wb-campaigns-v2 (тоже advert-api: count +
# adverts): наполняет campaign_products, без которой у promo нет пар
# advert×nm (на чистой БД таблицы просто нет — первый смоук упал 42P01).
# Конфиг: cmd/.configs/download-all/download-wb-promotion-v2-smoke-PG.yaml
# (статусы [9], full scan, days 3). БД по умолчанию wb_data_test — НЕ прод.
#
# Usage:
#   bash smoke-pg-promo.sh [--db wb_data_test] [--dry-run]
#
# Время: минуты (доминирует normquery_stats при 8/min: пары/100 запросов).
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
CFG="$PONCHO/cmd/.configs/download-all/download-wb-promotion-v2-smoke-PG.yaml"
LOGDIR="$PONCHO/logs"; mkdir -p "$LOGDIR"
STAMP="$(date +%Y-%m-%d-%H%M)"
LOG="$LOGDIR/smoke-pg-promo-$STAMP.log"

DB="wb_data_test"
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) DB="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    *) echo "Неизвестный флаг: $1 (см. шапку скрипта)"; exit 2 ;;
  esac
done

ENV_FILE="$PONCHO/.env"
if [[ -f "$ENV_FILE" ]]; then set -a; source "$ENV_FILE"; set +a; fi
: "${PG_PWD:?нет PG_PWD — загрузите .env}"

psql_db() {
  PGPASSWORD="$PG_PWD" psql -v ON_ERROR_STOP=1 -h "${PGHOST:-127.0.0.1}" -p "${PGPORT:-5432}" \
    -U "${PGUSER:-postgres}" -d "$DB" "$@"
}

if [[ "$DRY_RUN" == "1" ]]; then
  echo "--dry-run: план без запуска:"
  echo "  0. download-wb-campaigns-v2 --config download-wb-campaigns-v2-PG.yaml"
  echo "     пререквизит: наполняет campaign_products (на чистой БД её нет)"
  echo "  1. download-wb-promotion-v2 --config <smoke-PG> --backend postgres --db $DB"
  echo "     фазы: campaigns → normquery (stats/list/bids/minus) → budgets (v2)"
  echo "  2. вердикт 429 из лога: grep 'status 429' (фатальные vs ретраи);"
  echo "     если прогон упал — вердикт НЕДЕЙСТВИТЕЛЕН (запросов могло не быть)"
  echo "  3. SQL: campaign_budget+currency, normquery_clusters by kind,"
  echo "     normquery_bids инвариант floor + currency, normquery_stats окно"
  exit 0
fi

{
echo "══════════════════════════════════════════════════════════════"
echo "  SMOKE PG PROMO (4a4c92c) · БД: $DB · только advert-api"
echo "  $(date '+%Y-%m-%d %H:%M:%S') · лог: $LOG"
echo "══════════════════════════════════════════════════════════════"

echo ""
echo "── Phase 0: пререквизит — download-wb-campaigns-v2 (наполняет campaign_products) ──"
CAMP_RC=0
go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" \
  --config "$PONCHO/cmd/.configs/download-all/download-wb-campaigns-v2-PG.yaml" \
  --backend postgres --pg-database "$DB" 2>&1 || CAMP_RC=$?
if [[ "$CAMP_RC" != "0" ]]; then
  echo "  !! Phase 0 упал (exit $CAMP_RC) — без campaign_products прогон promo"
  echo "     бессмыслен (нет ни одной пары advert×nm). Чините причину, перезапустите."
  exit 1
fi

echo ""
echo "── Phase 1: download-wb-promotion-v2 (smoke-конфиг) ──"
PROMO_RC=0
go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" \
  --config "$CFG" --backend postgres --db "$DB" 2>&1 || PROMO_RC=$?

sleep 1   # дать tee дописать хвост go-run до grep'ов

echo ""
echo "── Phase 2: вердикт по 429 (лог прогона) ──"
TOTAL_429=$(grep -c "status 429" "$LOG" || true)
FATAL_429=$(grep "error:" "$LOG" | grep -c "status 429" || true)
OTHER_ERRS=$(grep "error:" "$LOG" | grep -vc "status 429" || true)
echo "  упоминаний 'status 429' всего:   $TOTAL_429  (включая пережитые ретраями)"
echo "  фатальных ошибок с 429:         $FATAL_429"
echo "  прочих ошибок:                  $OTHER_ERRS"
if [[ "$PROMO_RC" != "0" ]]; then
  echo "  !! ПРОГОН НЕ ДОШЁЛ ДО КОНЦА (exit $PROMO_RC) — ВЕРДИКТ ПО 429 НЕДЕЙСТВИТЕЛЕН:"
  echo "     до падения могло не быть ни одного запроса к advert-api."
elif [[ "$FATAL_429" == "0" && "$TOTAL_429" == "0" ]]; then
  echo "  ВЕРДИКТ: 429 НЕТ — ведра advert-api чистые в этом окне (прогон дошёл до конца)."
elif [[ "$FATAL_429" == "0" ]]; then
  echo "  ВЕРДИКТ: 429 были, но все пережиты ретраями/бэкоффом (адаптивный лимитер отработал)."
else
  echo "  ВЕРДИКТ: !! есть фатальные 429 — контекст ниже:"
  grep "error:" "$LOG" | grep "status 429" | head -5
fi
echo "  (exit прогона: $PROMO_RC)"

echo ""
echo "── Phase 3: SQL-проверки зафиксированного ──"

echo "── бюджет v2: строки сегодня + валюта (пусто = v2 не ответил/нет кампаний 9)"
psql_db -c "
SELECT advert_id, snapshot_date, total_budget, currency
FROM campaign_budget
WHERE snapshot_date = to_char(now() AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD')
ORDER BY advert_id LIMIT 10;"
psql_db -tAc "
SELECT 'campaign_budget сегодня: '||count(*)||' строк, currency: '||
       string_agg(DISTINCT currency, ',')
FROM campaign_budget
WHERE snapshot_date = to_char(now() AT TIME ZONE 'Europe/Moscow', 'YYYY-MM-DD');"

echo "── кластеры по kind: archived больше не теряются"
psql_db -c "
SELECT kind, count(*) AS rows, count(DISTINCT nm_id) AS nms
FROM normquery_clusters GROUP BY kind ORDER BY kind;"

echo "── ставки: инвариант floor (bid = bid_kopecks/100, усечение до 99 коп.) и валюта"
psql_db -c "
SELECT count(*) AS total,
       count(*) FILTER (WHERE currency = 'RUB') AS rub,
       count(*) FILTER (WHERE bid_kopecks > 0 AND
                        (bid_kopecks < bid*100 OR bid_kopecks >= (bid+1)*100)) AS floor_violations
FROM normquery_bids;"
psql_db -c "
SELECT advert_id, nm_id, normquery, bid, bid_kopecks, currency
FROM normquery_bids ORDER BY advert_id, nm_id LIMIT 5;"

echo "── stats: строки за окно (должны быть, если кампании 9 есть)"
psql_db -c "
SELECT stats_date, count(*) AS rows FROM normquery_stats
GROUP BY stats_date ORDER BY stats_date DESC LIMIT 5;"

echo ""
echo "════ Готово: $(date '+%H:%M:%S'). Полный лог: $LOG ════"
} 2>&1 | tee "$LOG"
