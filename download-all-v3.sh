#!/bin/bash
# WB Full Data Refresh v3 — PostgreSQL pipeline, writing to wb_data_prod
#
# Usage: bash download-all-v3.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# v3 uses --backend postgres with PG-specific config files (*-PG.yaml).
# All data goes to PostgreSQL wb_data_prod (192.168.10.7:15432).
#
# Phases ordered by speed: fast catalog → core sales → stock → advertising → slow analytics
# All configs in cmd/.configs/download-all/ (*-PG.yaml variants)

PONCHO="$(cd "$(dirname "$0")" && pwd)"
CONFIGS="$PONCHO/cmd/.configs/download-all"

# Lockfile: prevent concurrent runs
LOCKFILE="$PONCHO/.download-all-v3.lock"
exec 200>"${LOCKFILE}"
if ! flock -w 300 200; then
    echo "SKIP: another download process is running (lock: ${LOCKFILE})"
    exit 1
fi

DAYS="${1:-}"

echo "==========================================="
echo "  WB Full Data Download v3 (PostgreSQL)"
echo "==========================================="
echo "Root dir:   $PONCHO"
echo "Configs:    $CONFIGS (*-PG.yaml)"
echo "Database:   wb_data_prod (PostgreSQL 192.168.10.7:15432)"
echo "Backend:    postgres"
echo "Started:    $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase 1: Catalog (fast, high rate limits ~100 req/min) ──────────
# v2 downloaders: cards ✅, prices ✅, 1c-data ✅

echo ""
echo "── Phase 1: Catalog (cards, prices, 1C/PIM) ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" && go run . --config "$CONFIGS/download-wb-cards-v2-PG.yaml" --backend postgres) || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" && go run . --config "$CONFIGS/download-wb-prices-PG.yaml" --backend postgres) || exit $?
(cd "$PONCHO/cmd/data-downloaders/download-1c-data-v2" && go run . --config "$CONFIGS/download-1c-data-v2-PG.yaml" --backend postgres) || exit $?
# 1c-rests — internal API (optional)
#(cd "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" && go run . --config "$CONFIGS/download-1c-rests-PG.yaml" --backend postgres) || exit $?

echo "  Phase 1 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 2: Feedbacks (fast, separate API ~180 req/min) ────────────

echo ""
echo "── Phase 2: Feedbacks ──"
PHASE_START=$SECONDS

(cd "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" && go run . --config "$CONFIGS/download-wb-feedbacks-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 2 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 3: Sales & Revenue (core business data) ───────────────────

echo ""
echo "── Phase 3: Sales & Revenue ──"
PHASE_START=$SECONDS

### wb-orders v2 (early signal — cart/checkout, updates every 30 min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" && go run . --config "$CONFIGS/download-wb-orders-PG.yaml" --backend postgres) || exit $?
### wb-opsales v2 (operational sales/returns — preliminary data, updates every 30 min)
(cd "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" && go run . --config "$CONFIGS/download-wb-opsales-PG.yaml" --backend postgres) || exit $?
### wb-sales v2 (financial realization — daily)
(cd "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" && go run . --config "$CONFIGS/download-wb-sales-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-region-sales v2 (sales by region from Seller Analytics API)
(cd "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" && go run . --config "$CONFIGS/download-wb-region-sales-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 3 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 4: Stock & Logistics ──────────────────────────────────────

echo ""
echo "── Phase 4: Stock & Logistics ──"
PHASE_START=$SECONDS

### wb-stocks v2 (warehouse stock snapshot)
(cd "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" && go run . --config "$CONFIGS/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)) || exit $?
### wb-stock-history v2 (async CSV reports — stock dynamics per day)
(cd "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" && go run . --config "$CONFIGS/download-wb-stock-history-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-supplies v2 (FBW supplies: warehouses, tariffs, supply details — 7 Writer methods)
(cd "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" && go run . --config "$CONFIGS/download-wb-supplies-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 4 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 5: Advertising (moderate rate limits) ─────────────────────
# v2 downloaders: campaigns ✅, promotion-v2 ✅

echo ""
echo "── Phase 5: Advertising (campaigns, promotion-v2) ──"
PHASE_START=$SECONDS

### wb-campaigns v2 (basic 3 phases: campaigns, details, fullstats)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" && go run . --config "$CONFIGS/download-wb-campaigns-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-promotion-v2 (extended 14 phases: normquery, bids, finance, calendar — dual-backend)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" && go run . --config "$CONFIGS/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?

echo "  Phase 5 done in $(( SECONDS - PHASE_START ))s"

# ── Phase 6: Analytics (slow — 3 req/min shared limit) ──────────────

echo ""
echo "── Phase 6: Analytics (funnel, funnel-agg, funnel-csv, search-vis, penalties, whremains) ──"
PHASE_START=$SECONDS

### wb-funnel v2 (conversion funnel per product per day)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" && go run . --config "$CONFIGS/download-wb-funnel-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-funnel-agg v2 (aggregated funnel — period-level metrics, no nmID batching)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" && go run . --config "$CONFIGS/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-funnel-csv v2 (async CSV funnel export — detail/grouped)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" && go run . --config "$CONFIGS/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-search-vis v2 (search positions + queries — Seller Analytics API, 3 req/min)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" && go run . --config "$CONFIGS/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-penalties v2 (measurement penalties — Seller Analytics API, 1 req/min)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" && go run . --config "$CONFIGS/download-wb-penalties-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}) || exit $?
### wb-whremains v2 (warehouse remains — async 3-step, Seller Analytics API)
#(cd "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" && go run . --config "$CONFIGS/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)) || exit $?

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
echo "  Backend: postgres (wb_data_prod @ 192.168.10.7:15432)"
echo "  v2 downloaders (20): cards, prices, feedbacks, orders, opsales, sales,"
echo "    region-sales, stocks, stock-history, supplies, campaigns, promotion,"
echo "    funnel, funnel-agg, funnel-csv, search-vis, penalties, whremains, 1c-data, 1c-rests"
echo "==========================================="
