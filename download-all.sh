#!/bin/bash
# WB Full Data Refresh — all downloaders, single database
#
# Usage: bash download-all.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# Phases ordered by speed: fast catalog → core sales → stock → advertising → slow analytics
# All configs in cmd/.configs/download-all/, all data in /var/db/wb-sales.db

cd "$(dirname "$0")"

# Lockfile: prevent concurrent runs
LOCKFILE="$(pwd)/.download-all.lock"
exec 200>"${LOCKFILE}"
if ! flock -w 300 200; then
    echo "SKIP: another download process is running (lock: ${LOCKFILE})"
    exit 1
fi

DAYS="${1:-}"
CONFIG_DIR="cmd/.configs/download-all"

echo "==========================================="
echo "  WB Full Data Download"
echo "==========================================="
echo "Config dir: $CONFIG_DIR"
echo "Database:   /var/db/wb-sales.db"
echo "Started:    $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase 1: Catalog (fast, high rate limits ~100 req/min) ──────────

echo ""
echo "── Phase 1: Catalog (cards, prices, 1C/PIM) ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-cards && go run . --config ../../../$CONFIG_DIR/download-wb-cards.yaml) || exit $?
(cd cmd/data-downloaders/download-wb-prices && go run . --config ../../../$CONFIG_DIR/download-wb-prices.yaml) || exit $?
(cd cmd/data-downloaders/download-1c-data && go run . --config ../../../$CONFIG_DIR/download-1c-data.yaml) || exit $?

echo "  Phase 1 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 2: Feedbacks (fast, separate API ~180 req/min) ────────────

echo ""
echo "── Phase 2: Feedbacks ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-feedbacks && go run . --config ../../../$CONFIG_DIR/download-wb-feedbacks.yaml ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 2 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 3: Sales & Revenue (core business data) ───────────────────

echo ""
echo "── Phase 3: Sales & Revenue ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-sales && go run . --no-service --config ../../../$CONFIG_DIR/download-wb-sales.yaml ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-region-sales && go run . --config ../../../$CONFIG_DIR/download-wb-region-sales.yaml ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 3 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 4: Stock & Logistics ──────────────────────────────────────

echo ""
echo "── Phase 4: Stock & Logistics ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-stocks && go run . --config ../../../$CONFIG_DIR/download-wb-stocks.yaml --date $(date +%Y-%m-%d)) || exit $?
(cd cmd/data-downloaders/download-wb-stock-history && go run . --config ../../../$CONFIG_DIR/download-wb-stock-history.yaml ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-supplies && go run . --config ../../../$CONFIG_DIR/download-wb-supplies.yaml ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 4 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 5: Advertising (moderate rate limits) ─────────────────────

echo ""
echo "── Phase 5: Advertising ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-promotion && go run . --config ../../../$CONFIG_DIR/download-wb-promotion.yaml ${DAYS:+--days=$DAYS}) || exit $?

(cd cmd/data-downloaders/download-wb-promotion-v2 && go run . --config ../../../$CONFIG_DIR/download-wb-promotion-v2.yaml ${DAYS:+--days=$DAYS}) || exit $?

#echo "  Phase 5 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 6: Analytics (slow — 3 req/min shared limit) ──────────────

echo ""
echo "── Phase 6: Analytics (funnel, funnel-agg, search-visibility) ──"
PHASE_START=$SECONDS

(cd cmd/data-downloaders/download-wb-funnel && go run . --config ../../../$CONFIG_DIR/download-wb-funnel.yaml ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-funnel-agg && go run . --config ../../../$CONFIG_DIR/download-wb-funnel-agg.yaml ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-search-visibility && go run . --config ../../../$CONFIG_DIR/download-wb-search-visibility.yaml ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 6 done in $(( SECONDS - PHASE_START ))s"

# ── Summary ─────────────────────────────────────────────────────────

ELAPSED=$(( SECONDS - START ))
MINS=$(( ELAPSED / 60 ))
SECS=$(( ELAPSED % 60 ))
echo ""
echo "==========================================="
echo "  All downloads completed in ${MINS}m ${SECS}s"
echo "  Finished: $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
