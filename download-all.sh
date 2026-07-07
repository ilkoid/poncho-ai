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

export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

START=$SECONDS

###############################################################################
#  PASS 1: PostgreSQL
###############################################################################

PG_START=$SECONDS
echo ""
echo "═══════  Pass 1: PostgreSQL  ═══════"

# ── Phase 1: Catalog ──
echo "── Phase 1: Catalog ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-PG.yaml" --backend postgres

# ── Phase 2: Feedbacks ──
echo "── Phase 2: Feedbacks ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" --config "$C/download-wb-feedbacks-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 3: Sales & Revenue ──
echo "── Phase 3: Sales & Revenue ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" --config "$C/download-wb-orders-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" --config "$C/download-wb-opsales-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" --config "$C/download-wb-sales-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" --config "$C/download-wb-region-sales-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 4: Stock & Logistics ──
echo "── Phase 4: Stock & Logistics ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-metrics-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" --config "$C/download-wb-supplies-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 5: Advertising ──
echo "── Phase 5: Advertising ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 6: Analytics ──
echo "── Phase 6: Analytics ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" --config "$C/download-wb-funnel-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)

# ── Phase 7: PG Maintenance ──
echo "── Phase 7: PG Maintenance ──"

go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml"

PG_ELAPSED=$(( SECONDS - PG_START ))

###############################################################################
#  PASS 2: SQLite
###############################################################################

#SQLITE_START=$SECONDS
#echo ""
#echo "═══════  Pass 2: SQLite  ═══════"

# ── Phase 1: Catalog ──
#echo "── Phase 1: Catalog ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests.yaml" --backend sqlite

# ── Phase 2: Feedbacks ──
#echo "── Phase 2: Feedbacks ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" --config "$C/download-wb-feedbacks.yaml" --backend sqlite ${DAYS:+--days=$DAYS}

# ── Phase 3: Sales & Revenue ──
#echo "── Phase 3: Sales & Revenue ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" --config "$C/download-wb-orders.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" --config "$C/download-wb-opsales.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" --config "$C/download-wb-sales.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" --config "$C/download-wb-region-sales-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}

# ── Phase 4: Stock & Logistics ──
#echo "── Phase 4: Stock & Logistics ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)
#go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products.yaml" --backend sqlite --date $(date +%Y-%m-%d)
#go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-metrics.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" --config "$C/download-wb-supplies.yaml" --backend sqlite ${DAYS:+--days=$DAYS}

# ── Phase 5: Advertising ──
#echo "── Phase 5: Advertising ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}

# ── Phase 6: Analytics ──
#echo "── Phase 6: Analytics ──"

#go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" --config "$C/download-wb-funnel-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2.yaml" --backend sqlite ${DAYS:+--days=$DAYS}
#go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2.yaml" --backend sqlite
#go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)

#SQLITE_ELAPSED=$(( SECONDS - SQLITE_START ))

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
echo ""
echo "PG:     $((PG_ELAPSED / 60))m $((PG_ELAPSED % 60))s"
#echo "SQLite: $((SQLITE_ELAPSED / 60))m $((SQLITE_ELAPSED % 60))s"
#echo "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"
