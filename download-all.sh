#!/bin/bash
# WB Full Data Refresh — simple dual-backend (PostgreSQL + SQLite)
#
# Usage: bash download-all.sh [days]
#
# Как пользоваться:
#   1. Закомментируй строку с утилитой → она пропускается
#   2. Раскомментируй → снова работает
#   3.days — опционально, передаётся только в утилиты с --days
#
# Два прогона: сначала PostgreSQL, потом SQLite.
# Чтобы запустить только один бэкенд — закомментируй весь блок Pass 1 или Pass 2.
#
# В конце печатается Summary: список упавших утилит (⚠️ FAIL помечается сразу).
# Код выхода скрипта = 1, если хоть одна утилита упала.

export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

# ── Load .env if present (local-run support; harmless on VPS where env is exported) ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Single-instance lock (portable: mkdir is atomic on macOS & Linux; flock is macOS-absent) ──
LOCKDIR="$PONCHO/.download-all.lock"
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  echo "SKIP: другой прогон уже идёт (lock: $LOCKDIR)" >&2
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable (default 192.168.10.7:15432 unless overridden via .env) ──
PG_HOST="${PGHOST}"; PG_PORT="${PGPORT}"
if ! nc -z -w 5 "$PG_HOST" "$PG_PORT" 2>/dev/null; then
  echo "FAIL: PostgreSQL $PG_HOST:$PG_PORT недоступен. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env" >&2
  exit 1
fi

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
FAILED=()
RUN_COUNT=0

# run — обёртка над "go run <pkg> …". Имя утилиты = basename каталога из $3.
run() {
  local name; name="$(basename "$3")"
  RUN_COUNT=$((RUN_COUNT + 1))
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    echo "⚠️  FAIL: $name (exit $rc)" >&2
  fi
  return "$rc"
}

START=$SECONDS

###############################################################################
#  PASS 1: PostgreSQL
###############################################################################

PG_START=$SECONDS
echo ""
echo "═══════  Pass 1: PostgreSQL  ═══════"

# ── Phase 1: Catalog ──
echo "── Phase 1: Catalog ──"

run go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-PG.yaml" --backend postgres

# ── Phase 2: Feedbacks ──
echo "── Phase 2: Feedbacks ──"

run go run "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" --config "$C/download-wb-feedbacks-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 3: Sales & Revenue ──
echo "── Phase 3: Sales & Revenue ──"

run go run "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" --config "$C/download-wb-orders-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" --config "$C/download-wb-opsales-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" --config "$C/download-wb-sales-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" --config "$C/download-wb-region-sales-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 4: Stock & Logistics ──
echo "── Phase 4: Stock & Logistics ──"

run go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-metrics-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" --config "$C/download-wb-supplies-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-fbs-orders-v2" --config "$C/download-wb-fbs-orders-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 5: Advertising ──
echo "── Phase 5: Advertising ──"

run go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 6: Analytics ──
echo "── Phase 6: Analytics ──"

#run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" --config "$C/download-wb-funnel-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
run go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)

# ── Phase 7: PG Maintenance ──
echo "── Phase 7: PG Maintenance ──"

run go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml"

PG_ELAPSED=$(( SECONDS - PG_START ))

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
echo "PG:     $((PG_ELAPSED / 60))m $((PG_ELAPSED % 60))s"
#echo "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"

[ "${#FAILED[@]}" -eq 0 ] || exit 1
