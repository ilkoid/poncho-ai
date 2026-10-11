#!/bin/bash
# retro-whremains-rowkind.sh — ретро-разметка row_kind в warehouse_remains.
#
# Доразмечает СЛУЖЕБНЫЕ строки истории («Всего находится на складах», «В пути
# до получателей», «В пути возвраты на склад WB»), которые до 2026-10-11
# писались как обычные склады — источник двойного счёта остатков
# (аудит: dev_wb_stocks_data_notes.md; наивный SUM(quantity) давал ×2,0).
# Новые строки загрузчик размечает сам (ClassifyWarehouseRow), колонку и
# частичный индекс создаёт initWhRemainsSchema — скрипт трогает только историю.
#
# Безопасность:
#   - UPDATE батчами по snapshot_date, каждый батч — отдельный автокоммит
#     (без одной гигантской транзакции на 12-34 млн строк: длинные локи + bloat);
#   - идемпотентен: повторный запуск ничего не меняет (WHERE row_kind='warehouse'
#     + имена уже размеченных строк не матчатся);
#   - --dry-run показывает план без записи.
#
# Запускает ТОЛЬКО пользователь (запись в БД). Порядок: сначала локальная БД,
# затем VPS2 wb_data_prod.
#
# Usage:
#   bash retro-whremains-rowkind.sh [--db wb_data_prod] [--dry-run]
set -euo pipefail

DB="wb_data_prod"
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) DB="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    *) echo "Неизвестный флаг: $1"; exit 2 ;;
  esac
done

ENV_FILE="$(cd "$(dirname "$0")" && pwd)/.env"
if [[ -f "$ENV_FILE" ]]; then
  set -a; source "$ENV_FILE"; set +a
fi
: "${PGHOST:?нет PGHOST — загрузите .env}"
: "${PG_PWD:?нет PG_PWD — загрузите .env}"

psql_run() {
  PGPASSWORD="$PG_PWD" psql -v ON_ERROR_STOP=1 -h "$PGHOST" -p "${PGPORT:-5432}" \
    -U "${PGUSER:-postgres}" -d "$DB" "$@"
}

echo "── Ретро-разметка row_kind · БД: $DB ──"

# 0) Колонка должна существовать (создаёт initWhRemainsSchema при первом прогоне
#    download-wb-whremains-v2 с новой схемой); для --dry-run проверяем молча.
HAVE_COL=$(psql_run -tAc "SELECT count(*) FROM information_schema.columns
  WHERE table_name='warehouse_remains' AND column_name='row_kind'")
if [[ "$HAVE_COL" != "1" ]]; then
  echo "Колонки row_kind нет — сначала один прогон download-wb-whremains-v2 (схема добавит её) или вручную:"
  echo "  ALTER TABLE warehouse_remains ADD COLUMN IF NOT EXISTS row_kind TEXT NOT NULL DEFAULT 'warehouse';"
  exit 1
fi

# 1) Статистика до.
echo "── До ──"
psql_run -c "SELECT row_kind, count(*), min(snapshot_date) AS from_dt, max(snapshot_date) AS to_dt
  FROM warehouse_remains GROUP BY row_kind ORDER BY row_kind;"

# 2) Батчи по snapshot_date: служебные строки, ещё числящиеся 'warehouse'.
DATES=$(psql_run -tA -F$'\t' -c "
  SELECT DISTINCT snapshot_date FROM warehouse_remains
  WHERE row_kind = 'warehouse'
    AND (warehouse_name LIKE 'Всего%' OR warehouse_name LIKE 'В пути%')
  ORDER BY snapshot_date;")
TOTAL_DATES=$(printf %s "$DATES" | grep -c . || true)
if [[ -z "$DATES" ]]; then
  echo "История уже размечена — менять нечего."
else
  echo "Дней к доразметке: $TOTAL_DATES"
  if [[ "$DRY_RUN" == "1" ]]; then
    echo "--dry-run: показываю объём первого/последнего дня, не пишу."
    FIRST=$(echo "$DATES" | head -1); LAST=$(echo "$DATES" | tail -1)
    for D in "$FIRST" "$LAST"; do
      psql_run -c "SELECT '$D' AS day, count(*) AS service_rows FROM warehouse_remains
        WHERE snapshot_date='$D' AND row_kind='warehouse'
          AND (warehouse_name LIKE 'Всего%' OR warehouse_name LIKE 'В пути%');"
    done
  else
    n=0
    while IFS= read -r D; do
      [[ -z "$D" ]] && continue
      n=$((n+1))
      UPDATED=$(psql_run -tAc "
        UPDATE warehouse_remains SET row_kind = CASE
            WHEN warehouse_name LIKE 'Всего%'            THEN 'total'
            WHEN warehouse_name LIKE 'В пути возвраты%'  THEN 'in_transit_return'
            WHEN warehouse_name LIKE 'В пути%'           THEN 'in_transit_out'
          END
        WHERE snapshot_date='$D' AND row_kind='warehouse'
          AND (warehouse_name LIKE 'Всего%' OR warehouse_name LIKE 'В пути%')
        RETURNING 1" | wc -l | tr -d ' ')
      echo "  [$n/$TOTAL_DATES] $D — размечено $UPDATED строк"
    done <<< "$DATES"
  fi
fi

[[ "$DRY_RUN" == "1" ]] && { echo "── dry-run: запись и контроль пропущены ──"; exit 0; }

# 3) Контрольная сверка (док: SUM(row_kind='warehouse') ≤ total ±1, никогда кратно).
#    «Всего»-строка вложена в item ⇒ итог на (nm, tech_size), не на nm:
#    группируем по размеру, иначе несколько размеров = ложное «склады > итога».
echo "── Контроль (последний снапшот, топ-10 nm по строкам) ──"
psql_run -c "
WITH topnm AS (
  SELECT nm_id FROM warehouse_remains
  WHERE row_kind='warehouse' GROUP BY nm_id ORDER BY count(*) DESC LIMIT 10
), sums AS (
  SELECT nm_id, tech_size,
    SUM(quantity) FILTER (WHERE row_kind='warehouse') AS wh_sum,
    MAX(quantity) FILTER (WHERE row_kind='total')     AS total_row
  FROM warehouse_remains
  WHERE nm_id IN (SELECT nm_id FROM topnm)
    AND snapshot_date = (SELECT MAX(snapshot_date) FROM warehouse_remains)
  GROUP BY nm_id, tech_size
)
SELECT nm_id, tech_size, wh_sum, total_row,
  CASE WHEN total_row IS NULL THEN 'нет строки-итога'
       WHEN wh_sum <= total_row + 1 THEN 'ok'
       ELSE '!! ПРОВЕРИТЬ (склады > итога)' END AS verdict
FROM sums ORDER BY nm_id, tech_size;"

echo "── После ──"
psql_run -c "SELECT row_kind, count(*) FROM warehouse_remains GROUP BY row_kind ORDER BY row_kind;"
echo "Готово. «Итоговые» запросы остатков перевести на фильтр row_kind='warehouse' (частичный индекс idx_wr_warehouse_rows уже обслуживает)."
