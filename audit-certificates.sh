#!/usr/bin/env bash
# audit-certificates.sh — переиспользуемый аудит сертификатов карточек WB.
#
# Что делает:
#   1. Обновляет cards + card_characteristics (download-wb-cards-v2) и
#      onec_goods с certificate_* (download-1c-data-v2) в SQLite (/var/db/wb-sales.db).
#   2. Прогоняет fix-certificates в режимах --reconcile и --stage (БЕЗ --apply),
#      дампит каждый прогон в отдельные .diff/.csv файлы.
#   3. Печатает сводку по трём паттернам:
#        (а) нет сертификата в WB, в 1С есть        → --stage
#        (б) тип разрешительного документа не тот    → --reconcile (type swap)
#        (в) срок протух, в 1С новый                 → --reconcile (end-date)
#
# Скрипт НЕ делает --apply (запись в WB) — это делаешь ты сам, постепенно,
# через узкий scope в cmd/fix-utilities/fix-certificates/audit-config.yaml.
#
# Env: WB_API_CONTENT_KEY (cards-v2),
#      ONEC_API_URL + ONEC_PIM_URL (1c-data-v2),
#      WB_API_ANALYTICS_AND_PROMO_KEY или WB_API_KEY (фиксер: trash_filter + apply).

set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
DB="/var/db/wb-sales.db"
FIXER="$PONCHO/cmd/fix-utilities/fix-certificates"
CFG="$FIXER/audit-config.yaml"
CC="$PONCHO/cmd/.configs/download-all"
TODAY="$(date +%d.%m.%Y)"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="/tmp/audit-certificates-$STAMP"

# ─── 0. Проверка окружения ─────────────────────────────────────────────────
echo "▶ Проверка env…"
: "${WB_API_CONTENT_KEY:?нужен WB_API_CONTENT_KEY для download-wb-cards-v2}"
: "${ONEC_API_URL:?нужен ONEC_API_URL для download-1c-data-v2}"
: "${ONEC_PIM_URL:?нужен ONEC_PIM_URL для download-1c-data-v2}"
if [ -z "${WB_API_ANALYTICS_AND_PROMO_KEY:-}" ] && [ -z "${WB_API_KEY:-}" ]; then
  echo "Ошибка: нужен WB_API_ANALYTICS_AND_PROMO_KEY или WB_API_KEY для fix-certificates (trash_filter + apply)" >&2
  exit 1
fi
[ -f "$CFG" ] || { echo "Ошибка: нет $CFG (создай из config.yaml фиксёра)" >&2; exit 1; }

mkdir -p "$OUT"
echo "  OUT=$OUT"
echo "  DB =$DB"
echo "  CFG=$CFG"
echo "  --date=$TODAY (истёкшие сертификаты будут пропущены)"
echo

# ─── 1. Обновить данные (SQLite; конфиги уже на backend:sqlite + /var/db/wb-sales.db) ─
echo "▶ Phase 1/4: обновление cards + onec_goods…"
go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" \
    --config "$CC/download-wb-cards-v2.yaml" --backend sqlite
go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" \
    --config "$CC/download-1c-data-v2.yaml" --backend sqlite

echo "  Срез после загрузки:"
sqlite3 -readonly "$DB" \
  "SELECT '  cards: '       || COUNT(*) FROM cards;" \
  "SELECT '  onec_goods: '  || COUNT(*) FROM onec_goods WHERE has_certificate=1 AND certificate_number!='';" \
  "SELECT '  cert char-ы: ' || COUNT(*) FROM card_characteristics WHERE char_id IN (15001135,15001136);"
echo

# ─── 2. Reconcile (паттерны б + в + замена номера) ─────────────────────────
echo "▶ Phase 2/4: --reconcile (тип swap + срок + номер)…"
go run "$FIXER" --reconcile --config "$CFG" --date "$TODAY"
go run "$FIXER" --diff --config "$CFG" > "$OUT/reconcile.diff"
sqlite3 -readonly "$DB" ".mode csv" ".headers on" \
  "SELECT nm_id, vendor_code, subject_name, changes_json FROM fix_certificates_staging WHERE changes_json!='[]';" \
  > "$OUT/reconcile.csv"

# Метрики из staging (точные, через json_each по changes_json).
RECONCILE_TOTAL=$(sqlite3 -readonly "$DB" \
  "SELECT COUNT(*) FROM fix_certificates_staging WHERE changes_json!='[]';")
SWAP=$(sqlite3 -readonly "$DB" \
  "SELECT COUNT(DISTINCT s.nm_id) FROM fix_certificates_staging s, json_each(s.changes_json)
   WHERE s.changes_json!='[]' AND json_extract(value,'\$.new')='' AND json_extract(value,'\$.char_id') IN (15001135,15001136);")
NUM_MISMATCH=$(sqlite3 -readonly "$DB" \
  "SELECT COUNT(DISTINCT s.nm_id) FROM fix_certificates_staging s, json_each(s.changes_json)
   WHERE s.changes_json!='[]' AND json_extract(value,'\$.new')!='' AND json_extract(value,'\$.char_id') IN (15001135,15001136);")
DATE_MISMATCH=$(sqlite3 -readonly "$DB" \
  "SELECT COUNT(DISTINCT s.nm_id) FROM fix_certificates_staging s, json_each(s.changes_json)
   WHERE s.changes_json!='[]' AND json_extract(value,'\$.char_id') IN (15001137,15001138);")
echo "  reconcile-кандидатов: $RECONCILE_TOTAL (swap=$SWAP, номер=$NUM_MISMATCH, дата=$DATE_MISMATCH)"
echo

# ─── 3. Stage (паттерн а — пропуски) ───────────────────────────────────────
echo "▶ Phase 3/4: --stage (нет в WB, есть в 1С)…"
go run "$FIXER" --stage --config "$CFG" --date "$TODAY"
go run "$FIXER" --diff --config "$CFG" > "$OUT/stage.diff"
sqlite3 -readonly "$DB" ".mode csv" ".headers on" \
  "SELECT nm_id, vendor_code, subject_name, onec_certificate_number, onec_certificate_end FROM fix_certificates_staging WHERE changes_json!='[]';" \
  > "$OUT/stage.csv"
MISSING=$(sqlite3 -readonly "$DB" \
  "SELECT COUNT(*) FROM fix_certificates_staging WHERE changes_json!='[]';")
echo "  stage-кандидатов: $MISSING"
echo

# ─── 4. Сводка ─────────────────────────────────────────────────────────────
echo "▶ Phase 4/4: сводка…"
{
  echo "== Сводка сертификатных отклонений ($TODAY) =="
  echo
  echo "Паттерн                                              Кол-во"
  echo "------------------------------------------------------------"
  printf "(а) нет в WB, есть в 1С          (--stage):          %s\n" "$MISSING"
  printf "(б) тип swap cert↔decl           (--reconcile):      %s\n" "$SWAP"
  printf "(в) срок/номер расходится с 1С   (--reconcile):      номер=%s, дата=%s\n" "$NUM_MISMATCH" "$DATE_MISMATCH"
  echo "------------------------------------------------------------"
  printf "    всего reconcile-кандидатов (б+в+номер):          %s\n" "$RECONCILE_TOTAL"
  echo
  echo "Детально:"
  echo "  $OUT/reconcile.{diff,csv}   — кандидаты на исправление (б/в)"
  echo "  $OUT/stage.{diff,csv}       — кандидаты на заполнение (а)"
  echo
  echo "── Что дальше (применять ПОСТЕПЕННО, ты сам) ──"
  echo "1. Сузь scope в $CFG"
  echo "   (filters: vendor_codes / subject / seasons; поставь in_stock: true)"
  echo "2. Пересобери staging под узкий scope:"
  echo "   go run $FIXER --reconcile --config $CFG --date $TODAY"
  echo "   go run $FIXER --diff     --config $CFG"
  echo "3. Посмотри payload без отправки:"
  echo "   go run $FIXER --apply --dry-run --config $CFG"
  echo "4. ⚠ Применить (kizMarked-риск на одежде!):"
  echo "   go run $FIXER --apply            --config $CFG"
  echo
  echo "⚠ --apply полностью перезаписывает карточку. Smart Merge защищает"
  echo "  характеристики. kizMarked переносится ТОЛЬКО если cards.kiz_marked заполнен;"
  echo "  по умолчанию NULL → поле опускается → WB ставит default false → маркированные"
  echo "  (need_kiz=1) могут провалить модерацию. Начинай с НЕ-маркированных (Шапки,"
  echo "  Носки) или с type-swap, либо заполни cards.kiz_marked для суженного scope."
} | tee "$OUT/summary.txt"

echo
echo "✓ Готово. Сводка: $OUT/summary.txt"
