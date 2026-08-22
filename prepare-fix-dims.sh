#!/usr/bin/env bash
# prepare-fix-dims.sh — сборка локального SQLite-кеша для fix-card-dimensions-v1.5
#
# Наполняет изолированную SQLite-базу (~/dev/fix-dims.db) ровно теми таблицами,
# которые нужны fix-утилите. В отличие от PG, здесь реальный бренд (без санитайза):
#
#   1) 1С: onec_goods + onec_goods_sku + onec_dimensions   ← download-1c-data-v2
#   2) WB: cards + card_sizes + card_characteristics       ← download-wb-cards-v2
#      (sizes/characteristics нужны для LoadFullCard при --apply)
#   3) WB: stocks_daily_warehouses, снапшот за сегодня      ← download-wb-stocks-v2
#      (нужен для фильтра in_stock: true в config-fixdims.yaml fix-утилиты)
#
# Сама fix-утилита НЕ запускается — stage/diff/compare/apply выполняются вручную
# (готовые команды печатаются в конце работы скрипта).
#
# Usage:  bash prepare-fix-dims.sh
# Env:    секреты берутся из .env в корне репо
#         (ONEC_API_URL, ONEC_PIM_URL, WB_API_CONTENT_KEY, WB_API_KEY)

set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
DB="$HOME/dev/fix-dims.db"    # локальный кеш; живёт вне репо, не в git

# ── env ───────────────────────────────────────────────────────────────────────
if [[ ! -f "$PONCHO/.env" ]]; then
    echo "ERROR: $PONCHO/.env не найден" >&2
    exit 1
fi
set -a; source "$PONCHO/.env"; set +a

MISSING=()
for var in ONEC_API_URL ONEC_PIM_URL WB_API_CONTENT_KEY WB_API_KEY; do
    if [[ -z "${!var:-}" ]]; then
        MISSING+=("$var")
    fi
done
if ((${#MISSING[@]} > 0)); then
    echo "ERROR: в .env отсутствуют переменные: ${MISSING[*]}" >&2
    exit 1
fi

mkdir -p "$(dirname "$DB")"

echo "==========================================="
echo "  Prepare fix-dims cache (SQLite)"
echo "==========================================="
echo "Repo:     $PONCHO"
echo "Database: $DB"
echo "Started:  $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase 1: 1С — товары, SKU, габариты (апсерт, без --clean) ────────────────
echo ""
echo "── Phase 1: 1C goods + dimensions ──"
PHASE=$SECONDS
go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" \
    --config "$PONCHO/cmd/data-downloaders/download-1c-data-v2/config-fixdims.yaml" \
    --backend sqlite --db "$DB"
echo "  done in $(( SECONDS - PHASE ))s"

# ── Phase 2: WB карточки (реальный каталог, без скраба бренда) ───────────────
echo ""
echo "── Phase 2: WB cards ──"
PHASE=$SECONDS
go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" \
    --config "$PONCHO/cmd/data-downloaders/download-wb-cards-v2/config-fixdims.yaml" \
    --backend sqlite --db "$DB"
echo "  done in $(( SECONDS - PHASE ))s"

# ── Phase 3: WB остатки — один снапшот за сегодня (для фильтра in_stock) ─────
echo ""
echo "── Phase 3: WB stocks snapshot ──"
PHASE=$SECONDS
go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" \
    --config "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2/config-fixdims.yaml" \
    --backend sqlite --db "$DB" --date "$(date +%Y-%m-%d)"
echo "  done in $(( SECONDS - PHASE ))s"

# ── Сводка (только чтение) ────────────────────────────────────────────────────
echo ""
echo "==========================================="
echo "  Cache summary"
echo "==========================================="
# query_only: сессия read-only. Plain-open (не -readonly) обязателен: база в WAL,
# и без существующего -shm файла read-only соединение не открывается (ошибка 14).
if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$DB" <<'SQL'
PRAGMA query_only=ON;
SELECT 'onec_goods          ', count(*) FROM onec_goods;
SELECT 'onec_dimensions     ', count(*), max(created_at) FROM onec_dimensions;
SELECT 'cards               ', count(*) FROM cards;
SELECT 'stocks latest snap  ', count(*), max(snapshot_date) FROM stocks_daily_warehouses
WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses);
SQL
else
    echo "  (sqlite3 не найден — сводка пропущена)"
fi

ELAPSED=$(( SECONDS - START ))
FIXDIR="$PONCHO/cmd/fix-utilities/fix-card-dimensions-v1.5"
cat <<EOF

===========================================
  Done in $(( ELAPSED / 60 ))m $(( ELAPSED % 60 ))s
  Finished: $(date '+%Y-%m-%d %H:%M:%S')
===========================================

Дальше — вручную (строки полные, работают из любой папки):

#  set -a; source $PONCHO/.env; set +a      # ключи WB (нужны для --check/--apply)

#  go run $FIXDIR --compare --config $FIXDIR/config-fixdims.yaml --db $DB    # расхождения WB vs 1С
#  go run $FIXDIR --stage   --config $FIXDIR/config-fixdims.yaml --db $DB    # staging: пустые габариты
#  go run $FIXDIR --diff    --config $FIXDIR/config-fixdims.yaml --db $DB    # before/after
#  go run $FIXDIR --check   --config $FIXDIR/config-fixdims.yaml             # ошибки WB (API)

#  go run $FIXDIR --apply --dry-run --config $FIXDIR/config-fixdims.yaml --db $DB   # payload без отправки
#  go run $FIXDIR --apply --config $FIXDIR/config-fixdims.yaml --db $DB             # ⚠️ запись в WB API
EOF
