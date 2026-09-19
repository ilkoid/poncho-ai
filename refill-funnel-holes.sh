#!/bin/bash
# refill-funnel-holes.sh — ночной самодолив дырок funnel_metrics_aggregated.
#
# Проблема: каждая ночь пишет СВОЁ новое недельное окно (selected + past).
# Если ночная funnel-agg умерла в 429-шторме — окно отсутствует/частично
# навсегда, самолечения нет (доказано ночами 10-18.09.2026).
#
# Логика: для 21 ожидаемого окна [сегодня-27 .. сегодня-7] считаем строки;
# медиана полных окон — эталон; окно с числом строк < 80% медианы или
# отсутствующее — дырка. Дырки перекачиваются с явными датами
# (--selected-start/--selected-end, past выводится автоматически).
# Идемпотентно: upsert по (nm_id, period_start, period_end).
#
# Вызывается из download-all.sh (Phase 6, сразу после funnel-agg) — только
# ночью, днём НЕ гонять (мешает BI). Нефатально для ночного прогона.
#
# Usage:
#   bash refill-funnel-holes.sh             # рабочий прогон
#   bash refill-funnel-holes.sh --dry-run   # только показать дырки (без API)
set -uo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
LOGDIR="$PONCHO/logs"
TODAY=$(date +%F)
LOG="$LOGDIR/funnel-holes-$TODAY.log"
MAX_HOLES=4          # предохранитель: до 4 окон за прогон (~60-80 мин)
DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

[ -f "$PONCHO/.env" ] && { set -a; . "$PONCHO/.env"; set +a; }
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

PSQL="$(command -v psql || echo /opt/homebrew/opt/libpq/bin/psql)"
PGH="${PGHOST:-192.168.10.7}"; PGP="${PGPORT:-15432}"; PGU="${PGUSER:-postgres}"
DB="${FUNNEL_DB:-wb_data_prod}"

mkdir -p "$LOGDIR"
{
echo "=== $(date '+%F %T') refill-funnel-holes: старт (dry_run=$DRY_RUN) ==="

# --- Детекция дырок одним запросом -----------------------------------------
# Ожидаемые старты окон: current_date-27 .. current_date-7 (21 шт; глубина
# больше горизонта детекции — старые дырки выпадают из долива навсегда).
# Эталон — медиана строк окон с count>1000 (полных); дырка — <80% медианы.
HOLES=$(PGPASSWORD="$PG_PWD" "$PSQL" -h "$PGH" -p "$PGP" -U "$PGU" -d "$DB" -At -F'|' -c "
WITH expect AS (
  SELECT generate_series(current_date - 27, current_date - 7, interval '1 day')::date AS s
), have AS (
  SELECT period_start::date AS s, count(*) AS c
  FROM funnel_metrics_aggregated GROUP BY 1
), med AS (
  SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY c)::int AS m
  FROM have JOIN expect USING (s) WHERE c > 1000
)
SELECT e.s, COALESCE(h.c, 0), med.m
FROM expect e LEFT JOIN have h USING (s) CROSS JOIN med
WHERE COALESCE(h.c, 0) < 0.8 * med.m
ORDER BY e.s") || { echo "FAIL: запрос к PG"; exit 1; }

if [ -z "$HOLES" ]; then
  echo "дырок нет — все 14 окон за 2 недели полные"
  exit 0
fi

echo "найдены дырки (старт|строк|эталон):"; echo "$HOLES" | sed 's/^/  /'

if [ "$DRY_RUN" -eq 1 ]; then
  N=$(echo "$HOLES" | wc -l | tr -d ' ')
  echo "dry-run: долив отключён, дырок=$N"
  exit 0
fi

# --- Долив: по окну за раз, явными датами ----------------------------------
fail=0; done_n=0
while IFS='|' read -r S CNT REF; do
  [ $done_n -ge $MAX_HOLES ] && { echo "достигнут лимит $MAX_HOLES окон — остальное в следующий прогон"; break; }
  END=$(date -j -v+6d -f "%Y-%m-%d" "$S" "+%Y-%m-%d")
  echo "--- долив окна $S → $END (сейчас $CNT строк, эталон $REF) ---"
  go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" \
    --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres \
    --selected-start "$S" --selected-end "$END"
  rc=$?
  echo "--- окно $S rc=$rc ---"
  if [ $rc -ne 0 ]; then fail=$((fail+1)); else done_n=$((done_n+1)); fi
  # Пауза между окнами: лимит эндпоинта 2 req/min, штормы — даём WB выдохнуть.
  sleep 60
done <<< "$HOLES"

echo "=== $(date '+%F %T') refill-funnel-holes: финиш, залито=$done_n, ошибок=$fail ==="
# Итог в статус-лог ночей (однострочный дайджест).
if [ $fail -gt 0 ]; then
  echo "[$(date '+%F %T')] REFILL-HOLES funnel-agg: дырок найдено $(echo "$HOLES" | wc -l | tr -d ' '), залито $done_n, НЕ залито $fail — см. funnel-holes-$TODAY.log" >> "$LOGDIR/nightly-status.log"
  exit 1
fi
echo "[$(date '+%F %T')] REFILL-HOLES funnel-agg: дырок $(echo "$HOLES" | wc -l | tr -d ' '), все залиты — см. funnel-holes-$TODAY.log" >> "$LOGDIR/nightly-status.log"
} >> "$LOG" 2>&1
