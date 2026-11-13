#!/bin/bash
# WB Day Snapshots — ручной «спасатель дня»: ТОЛЬКО утилиты с датированными срезами,
# которые НЕ самовосстанавливаются ближайшей ночной сеткой.
#
# Сценарий: ночная сетка на Маке умерла (PG недоступен, ребут, что угодно — оба ночных
# блока упали) → днём запускаешь вручную в терминале:
#     caffeinate -is bash download-day-snapshots.sh
# и датированные срезы за сегодня сохранены. ~2–4 ч днём (вселенная кампаний жирная,
# шторм 429; все утилиты на схеме «подсказка+ε» + rescue, как в ночных блоках).
#
# ЧТО ЗДЕСЬ (пропустил день = дырка навсегда или надолго):
#   Phase 1 «Быстрые срезы»: stocks, stock-products, whremains (--date сегодня), item-rating
#   Phase 2 «Промо-дата»:    campaigns → promotion (normquery stats за день)
#                            → funnel-agg → search-vis (снапшот дня)
# Чего ЗДЕСЬ НЕТ (текущее состояние / окна — догонит ближайшая ночь): cards, prices,
#   1C, sales, orders, feedbacks, penalties, funnel-csv, maint/ANALYZE.
#
# Usage: bash download-day-snapshots.sh [days]   # days → --days промо-утилитам, дефолт из yaml
# Порядок Phase 2 не менять: promotion читает campaign_products свежего campaigns.
# Днём пересекается по ведрам WB с промо VPS2 (10:00–19:00) — переживёт, но медленнее;
# спокойно можно после ~19:00.

export PGHOST="${PGHOST:-localhost}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

# go run <пакет> требует cwd внутри модуля
cd "$PONCHO" || { echo "FAIL: не могу зайти в $PONCHO" >&2; exit 1; }

# ── Load .env if present ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Single-instance lock: свой каталог; протухший (>4ч) — убрать ──
LOCKDIR="$PONCHO/.download-day-snapshots.lock"
find "$PONCHO" -maxdepth 1 -name '.download-day-snapshots.lock' -type d -mmin +240 -exec rmdir {} \; 2>/dev/null
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  echo "SKIP: другой прогон уже идёт (lock: $LOCKDIR)" >&2
  exit 0
fi
touch "$LOCKDIR"   # mtime = момент старта; шаги ниже обновляют его
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

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
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
echo "═══════  WB Day Snapshots (ручной спасатель датированных срезов)  ═══════"

# ── Phase 1: быстрые датированные срезы — сначала самое невосполнимое и дешёвое ──
echo "── Phase 1: Быстрые срезы (stocks / stock-products / whremains / item-rating) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-item-rating-v2" --config "$C/download-wb-item-rating-v2-PG.yaml" --backend postgres

# ── Phase 2: промо-срезы с датой (вселенная → stats за день → воронка → search-vis) ──
echo "── Phase 2: Campaigns (вселенная пар для promotion) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres

echo "── Phase 2: Promotion (normquery stats за день) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

echo "── Phase 2: Funnel-agg (дневные строки воронки) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

echo "── Phase 2: Search visibility (снапшот дня) ──"
run go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

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
