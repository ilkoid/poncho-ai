#!/bin/bash
# WB Full Data Refresh v2 — v2 downloaders with PG support, writing to SQLite
#
# Usage: bash download-all-v2.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# v2 downloaders support --backend postgres. This script uses --backend sqlite
# (default) for safety. To switch to PostgreSQL: change --backend sqlite →
# --backend postgres for each phase.
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
# v2 downloaders: cards ✅, prices ✅, 1c-data ✅
# v1 downloaders: 1c-rests (no v2 — internal API, not WB)

echo ""
echo "── Phase 1: Catalog (cards, prices, 1C/PIM) ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" && go run . --config "$CONFIGS/download-wb-cards-v2.yaml" --backend sqlite) || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" && go run . --config "$CONFIGS/download-wb-prices.yaml" --backend sqlite) || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-1c-data-v2" && go run . --config "$CONFIGS/download-1c-data-v2.yaml" --backend sqlite) || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-1c-rests" && go run . --config "$CONFIGS/download-1c-rests.yaml") || exit $?

echo "  Phase 1 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 2: Feedbacks (fast, separate API ~180 req/min) ────────────
# v2 downloader: feedbacks ✅

echo ""
echo "── Phase 2: Feedbacks ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" && go run . --config "$CONFIGS/download-wb-feedbacks.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 2 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 3: Sales & Revenue (core business data) ───────────────────
# v2 downloaders: orders ✅, opsales ✅, sales ✅, region-sales ✅

echo ""
echo "── Phase 3: Sales & Revenue ──"
PHASE_START=$SECONDS

### wb-orders v2 (early signal — cart/checkout, updates every 30 min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" && go run . --config "$CONFIGS/download-wb-orders.yaml" --backend sqlite) || exit $?
### wb-opsales v2 (operational sales/returns — preliminary data, updates every 30 min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" && go run . --config "$CONFIGS/download-wb-opsales.yaml" --backend sqlite) || exit $?
### wb-sales v2 (financial realization — daily)
(cd "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" && go run . --config "$CONFIGS/download-wb-sales.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-region-sales v2 (sales by region from Seller Analytics API)
(cd "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" && go run . --config "$CONFIGS/download-wb-region-sales-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 3 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 4: Stock & Logistics ──────────────────────────────────────
# v2 downloaders: stocks ✅, stock-history ✅, supplies ✅

echo ""
echo "── Phase 4: Stock & Logistics ──"
PHASE_START=$SECONDS

### wb-stocks v2 (warehouse stock snapshot)
(cd "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" && go run . --config "$CONFIGS/download-wb-stocks-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)) || exit $?
### wb-stock-history v2 (async CSV reports — stock dynamics per day)
(cd "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" && go run . --config "$CONFIGS/download-wb-stock-history.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-supplies v2 (FBW supplies: warehouses, tariffs, supply details — 7 Writer methods)
(cd "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" && go run . --config "$CONFIGS/download-wb-supplies.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 4 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 5: Advertising (moderate rate limits) ─────────────────────
# v2 downloaders: campaigns ✅, promotion-v2 ✅
# promotion-v2: 14-phase extended (normquery, bids, finance, calendar — dual-backend)

echo ""
echo "── Phase 5: Advertising (campaigns, promotion-v2) ──"
PHASE_START=$SECONDS

### wb-campaigns v2 (basic 3 phases: campaigns, details, fullstats)
(cd "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" && go run . --config "$CONFIGS/download-wb-campaigns-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-promotion-v2 (extended 14 phases: normquery, bids, finance, calendar — dual-backend)
(cd "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" && go run . --config "$CONFIGS/download-wb-promotion-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 5 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 6: Analytics (slow — 3 req/min shared limit) ──────────────
# v2 downloaders: funnel ✅, funnel-agg ✅, funnel-csv ✅, search-vis ✅

echo ""
echo "── Phase 6: Analytics (funnel, funnel-agg, funnel-csv, search-visibility) ──"
PHASE_START=$SECONDS

### wb-funnel v2 (conversion funnel per product per day)
(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" && go run . --config "$CONFIGS/download-wb-funnel-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-funnel-agg v2 (aggregated funnel — period-level metrics, no nmID batching)
(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" && go run . --config "$CONFIGS/download-wb-funnel-agg.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-funnel-csv v2 (async CSV funnel export — detail/grouped)
(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" && go run . --config "$CONFIGS/download-wb-funnel-csv-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?
### wb-search-vis v2 (search positions + queries — Seller Analytics API, 3 req/min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" && go run . --config "$CONFIGS/download-wb-search-vis-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}) || exit $?

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
echo "  v2 downloaders (PG-ready, 17): cards, prices, feedbacks, orders, opsales, sales,"
echo "    region-sales, stocks, stock-history, supplies, campaigns, promotion,"
echo "    funnel, funnel-agg, funnel-csv, search-vis, 1c-data"
echo "  v1 downloaders (SQLite, 1):    1c-rests"
echo "==========================================="
