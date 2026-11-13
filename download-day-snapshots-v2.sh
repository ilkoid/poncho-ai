#!/bin/bash
# WB/1C Day Snapshots v2 — ручной «спасатель дня», версия 2 (без промо-семьи).
#
# Сценарий: ночная сетка умерла → днём/вечером руками в терминале:
#     caffeinate -is bash download-day-snapshots-v2.sh
#
# ПОРЯДОК = зависимости данных (поправка пользователя: карточки ПЕРВЫМИ —
# новым товарам нужно «куда класть» цены и остатки):
#   Phase 1: cards — каталог (обновление; заодно долечивает частичный снимок).
#   Phase 2: 1c-data (товары/цены 1С/PIM) → 1c-rests (остатки 1С «сейчас»; срез, истории нет).
#   Phase 3: prices (WB) — ложатся на свежий каталог.
#   Phase 4: stocks / stock-products / whremains — остатки WB на дату среза.
#   Phase 5 (хвост, не дейт-критичен): item-rating — динамика оценок, окно ретро-покроет.
#
# ДАТА СРЕЗА фиксируется ОДИН РАЗ на старте (DATE ниже) и одна на все --date утилиты:
# даже если фазы уедут за полночь, строки остатков всё равно лягут на дату спасаемого дня.
#
# ПРОМО-СЕМЬЯ (campaigns/promotion/funnel/search-vis) НАМЕРЕННО НЕТ — штормовые ведра.
#
# Usage: bash download-day-snapshots-v2.sh
# Все утилиты идемпотентны (upsert/полная перезагрузка) — повторный запуск безопасен.

export PGHOST="${PGHOST:-localhost}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"

# ── Дата среза: один раз на старте, одна на все --date ──
DATE="$(date +%Y-%m-%d)"

cd "$PONCHO" || { echo "FAIL: не могу зайти в $PONCHO" >&2; exit 1; }

# ── Load .env if present ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Single-instance lock: свой каталог; протухший (>4ч) — убрать ──
LOCKDIR="$PONCHO/.download-day-snapshots-v2.lock"
find "$PONCHO" -maxdepth 1 -name '.download-day-snapshots-v2.lock' -type d -mmin +240 -exec rmdir {} \; 2>/dev/null
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  echo "SKIP: другой прогон уже идёт (lock: $LOCKDIR)" >&2
  exit 0
fi
touch "$LOCKDIR"
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable ──
PG_HOST="${PGHOST}"; PG_PORT="${PGPORT}"
PG_ISREADY="$(command -v pg_isready || echo /opt/homebrew/opt/libpq/bin/pg_isready)"
if [ -x "$PG_ISREADY" ]; then
  "$PG_ISREADY" -h "$PG_HOST" -p "$PG_PORT" -t 5 >/dev/null 2>&1 || PG_DOWN=1
else
  nc -z -w 5 "$PG_HOST" "$PG_PORT" 2>/dev/null || PG_DOWN=1
fi
if [ "${PG_DOWN:-0}" = "1" ]; then
  echo "FAIL: PostgreSQL $PG_HOST:$PG_PORT не отвечает. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env" >&2
  exit 1
fi

# ── Failure tracking ──
FAILED=()
RUN_COUNT=0

run() {
  local name; name="$(basename "$3")"
  RUN_COUNT=$((RUN_COUNT + 1))
  touch "$LOCKDIR" 2>/dev/null || true
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    echo "⚠️  FAIL: $name (exit $rc)" >&2
  fi
  return "$rc"
}

START=$SECONDS
echo ""
echo "═══════  WB/1C Day Snapshots v2 — срез дня $DATE (без промо)  ═══════"

# ── Phase 1: cards — каталог ПЕРВЫМ: новым товарам нужно «куда класть» цены/остатки ──
echo "── Phase 1: Cards (каталог) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres

# ── Phase 2: 1С — сначала товары/цены, потом остатки (rests ссылаются на товары) ──
echo "── Phase 2: 1C (товары/цены → остатки) ──"
run go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-PG.yaml" --backend postgres

# ── Phase 3: prices (WB) — на свежий каталог ──
echo "── Phase 3: Prices (WB) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices-PG.yaml" --backend postgres

# ── Phase 4: остатки WB на дату среза $DATE (задним числом WB не отдаст) ──
echo "── Phase 4: Stocks-срез на $DATE ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date "$DATE"
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date "$DATE"
run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date "$DATE"

# ── Phase 5: хвост — динамика оценок (не дейт-критично; после полуночи тоже ок) ──
echo "── Phase 5: item-rating (можно прервать без потери дня) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-item-rating-v2" --config "$C/download-wb-item-rating-v2-PG.yaml" --backend postgres

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
echo ""
echo "═══════  Summary  ═══════"
if [ "${#FAILED[@]}" -eq 0 ]; then
  echo "✔ Все утилиты завершились успешно ($RUN_COUNT/$RUN_COUNT)"
else
  echo "✗ Не выполнились (${#FAILED[@]} из $RUN_COUNT):"
  printf '  ✗ %s\n' "${FAILED[@]}"
fi
echo "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"
[ "${#FAILED[@]}" -eq 0 ] || exit 1
