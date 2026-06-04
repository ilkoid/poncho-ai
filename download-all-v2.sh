#!/bin/bash
# WB Full Data Refresh v2 — v2 downloaders with PG support, writing to SQLite
#
# Usage: bash download-all-v2.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# v2 downloaders (cards, prices, orders, opsales, sales, stocks, funnel, feedbacks, campaigns, searchvis, funnel-csv) support
# --backend postgres. This script uses --backend sqlite (default) for safety.
# To switch to PostgreSQL: change --backend sqlite → --backend postgres for each phase.
#
# Phases ordered by speed: fast catalog → core sales → stock → advertising → slow analytics
# All configs in cmd/.configs/download-all/, all data in /var/db/wb-sales.db

PONCHO="$(cd "$(dirname "$0")" && pwd)"
CONFIGS="$PONCHO/cmd/.configs/download-all"

# Lockfile: prevent concurrent runs
LOCKFILE="$PONCHO/.download-all-v2.lock"
exec 200>"${LOCKFILE}"
if ! flock -w 300 200; then
    echo "SKIP: another download process is running (lock: ${LOCKFILE})"
    exit 1
fi

DAYS="${1:-}"

echo "==========================================="
echo "  WB Full Data Download v2"
echo "==========================================="
echo "Root dir:   $PONCHO"
echo "Configs:    $CONFIGS"
echo "Database:   /var/db/wb-sales.db"
echo "Backend:    sqlite (v2 downloaders PG-ready)"
echo "Started:    $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase 1: Catalog (fast, high rate limits ~100 req/min) ──────────
# v2 downloaders: cards ✅, prices ✅
# v1 downloaders: 1c-data, 1c-rests (no PG adapter yet)

echo ""
echo "── Phase 1: Catalog (cards, prices, 1C/PIM) ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" && go run . --config "$CONFIGS/download-wb-cards-v2.yaml" --backend sqlite) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" && go run . --config "$CONFIGS/download-wb-prices.yaml") || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-1c-data" && go run . --config "$CONFIGS/download-1c-data.yaml") || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-1c-rests" && go run . --config "$CONFIGS/download-1c-rests.yaml") || exit $?

echo "  Phase 1 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 2: Feedbacks (fast, separate API ~180 req/min) ────────────
# v2 downloader: feedbacks ✅

echo ""
echo "── Phase 2: Feedbacks ──"
PHASE_START=$SECONDS

#(cd "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" && go run . --config "$CONFIGS/download-wb-feedbacks.yaml" ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 2 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 3: Sales & Revenue (core business data) ───────────────────
# v2 downloaders: orders ✅, opsales ✅, sales ✅, region-sales ✅

echo ""
echo "── Phase 3: Sales & Revenue ──"
PHASE_START=$SECONDS

### wb-orders v2 (early signal — cart/checkout, updates every 30 min)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" && go run . --config "$CONFIGS/download-wb-orders.yaml" --backend sqlite) || exit $?
### wb-opsales v2 (operational sales/returns — preliminary data, updates every 30 min)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" && go run . --config "$CONFIGS/download-wb-opsales.yaml" --backend sqlite) || exit $?
### wb-sales v2
#(cd "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" && go run . --config "$CONFIGS/download-wb-sales.yaml" ${DAYS:+--days=$DAYS}) || exit $?
### wb-region-sales v2 (sales by region from Seller Analytics API)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" && go run . --config "$CONFIGS/download-wb-region-sales-v2.yaml" ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 3 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 4: Stock & Logistics ──────────────────────────────────────
# v2 downloader: stocks ✅
# v1 downloaders: stock-history, supplies (no PG adapter yet)

echo ""
echo "── Phase 4: Stock & Logistics ──"
PHASE_START=$SECONDS

### wb-stocks v2 (warehouse stock snapshot)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" && go run . --config "$CONFIGS/download-wb-stocks-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-stock-history" && go run . --config "$CONFIGS/download-wb-stock-history.yaml" ${DAYS:+--days=$DAYS}) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-supplies" && go run . --config "$CONFIGS/download-wb-supplies.yaml" ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 4 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 5: Advertising (moderate rate limits) ─────────────────────
# v2 downloader: campaigns ✅
# v1 downloader: promotion-v2 (14-phase extended — SQLite only)

echo ""
echo "── Phase 5: Advertising (campaigns, promotion-v2) ──"
PHASE_START=$SECONDS

### wb-campaigns v2 (basic 3 phases: campaigns, details, fullstats)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" && go run . --config "$CONFIGS/download-wb-campaigns-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-promotion-v2 (extended 14 phases: normquery, bids, finance, calendar — SQLite only)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" && go run . --config "$CONFIGS/download-wb-promotion-v2.yaml" ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 5 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 6: Analytics (slow — 3 req/min shared limit) ──────────────
# v2 downloaders: funnel ✅, searchvis ✅, funnel-csv ✅
# v1 downloaders: funnel-agg (no PG adapter yet)

echo ""
echo "── Phase 6: Analytics (funnel, funnel-agg, search-visibility) ──"
PHASE_START=$SECONDS

### wb-funnel v2 (conversion funnel per product per day)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" && go run . --config "$CONFIGS/download-wb-funnel-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv" && go run . --config "$CONFIGS/download-wb-funnel-csv.yaml" ${DAYS:+--days=$DAYS}) || exit $?
### wb-funnel-csv v2 (async CSV funnel export — detail/grouped)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" && go run . --config "$CONFIGS/download-wb-funnel-csv-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg" && go run . --config "$CONFIGS/download-wb-funnel-agg.yaml" ${DAYS:+--days=$DAYS}) || exit $?
#(cd "$PONCHO/cmd/data-downloaders/download-wb-search-visibility" && go run . --config "$CONFIGS/download-wb-search-visibility.yaml" ${DAYS:+--days=$DAYS}) || exit $?
### wb-search-vis v2 (search positions + queries — Seller Analytics API, 3 req/min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" && go run . --config "$CONFIGS/download-wb-search-vis-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

#(cd "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" && go run . --config "$CONFIGS/download-wb-orders.yaml" --backend sqlite) || exit $?
	
echo "  Phase 6 done in $(( SECONDS - PHASE_START ))s"

# ── Summary ─────────────────────────────────────────────────────────

ELAPSED=$(( SECONDS - START ))
MINS=$(( ELAPSED / 60 ))
SECS=$(( ELAPSED % 60 ))
echo ""
echo "==========================================="
echo "  All downloads completed in ${MINS}m ${SECS}s"
echo "  Finished: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""
echo "  v2 downloaders (PG-ready): cards, prices, feedbacks, orders, opsales, sales, region-sales, stocks, funnel, campaigns, searchvis, funnel-csv"
echo "  v1 downloaders (SQLite):   1c-data, 1c-rests, stock-history, supplies, promotion-v2, funnel-agg"
echo "==========================================="
