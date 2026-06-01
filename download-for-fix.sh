#!/bin/bash
# WB Full Data Refresh — all downloaders, single database
#
# Usage: bash download-all.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# Phases ordered by speed: fast catalog → core sales → stock → advertising → slow analytics
# All configs in cmd/.configs/download-all/, all data in /var/db/wb-sales.db

PONCHO="$(cd "$(dirname "$0")" && pwd)"
CONFIGS="$PONCHO/cmd/.configs/download-all"

# Lockfile: prevent concurrent runs
LOCKFILE="$PONCHO/.download-all.lock"
exec 200>"${LOCKFILE}"
if ! flock -w 300 200; then
    echo "SKIP: another download process is running (lock: ${LOCKFILE})"
    exit 1
fi

DAYS="${1:-}"

echo "==========================================="
echo "  WB Full Data Download"
echo "==========================================="
echo "Root dir:   $PONCHO"
echo "Configs:    $CONFIGS"
echo "Database:   /var/db/wb-sales.db"
echo "Started:    $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase 1: Catalog (fast, high rate limits ~100 req/min) ──────────

echo ""
echo "── Phase 1: Catalog (cards, prices, 1C/PIM) ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" && go run . --config "$CONFIGS/download-wb-cards.yaml") || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" && go run . --config "$CONFIGS/download-wb-sales.yaml" ${DAYS:+--days=$DAYS}) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-prices" && go run . --config "$CONFIGS/download-wb-prices.yaml") || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-1c-data" && go run . --config "$CONFIGS/download-1c-data.yaml") || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-1c-rests" && go run . --config "$CONFIGS/download-1c-rests.yaml") || exit $?

echo "  Phase 1 done in $(( SECONDS - PHASE_START ))s"
# ── Summary ─────────────────────────────────────────────────────────

ELAPSED=$(( SECONDS - START ))
MINS=$(( ELAPSED / 60 ))
SECS=$(( ELAPSED % 60 ))
echo ""
echo "==========================================="
echo "  All downloads completed in ${MINS}m ${SECS}s"
echo "  Finished: $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
