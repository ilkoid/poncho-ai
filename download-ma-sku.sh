#!/bin/bash
# Minimal data refresh for build-ma-sku-snapshots utility
# Only downloads tables required for SKU-level stock analysis
#
# Required tables → downloaders:
#   card_sizes, products     → download-wb-cards
#   onec_goods, pim_goods    → download-1c-data
#   sales                    → download-wb-sales
#   stocks_daily_warehouses  → download-wb-stocks
#   supplies, supply_goods   → download-wb-supplies
#
# Usage: bash download-ma-sku.sh [days]
#   days  - override --days for sales (default: from config.yaml)

cd "$(dirname "$0")"

DAYS="${1:-}"
CONFIG_DIR="cmd/.configs"

echo "=== MA SKU Data Download ==="
echo "Started: $(date '+%Y-%m-%d %H:%M:%S')"
START=$SECONDS

# Phase 1: Fast — catalog + attributes (high rate limits)
echo "--- Cards + 1C/PIM catalog ---"
(cd cmd/data-downloaders/download-wb-cards && go run . --config ../../../$CONFIG_DIR/download-wb-cards.yaml) || exit $?
(cd cmd/data-downloaders/download-1c-data && go run . --config ../../../$CONFIG_DIR/download-1c-data.yaml) || exit $?

# Phase 2: Sales for MA computation
#echo "--- Sales ---"
(cd cmd/data-downloaders/download-wb-sales && go run . --config ../../../$CONFIG_DIR/download-wb-sales.yaml ${DAYS:+--days=$DAYS}) || exit $?

# Phase 3: Stock snapshots (depends on cards being loaded)
echo "--- Stock snapshots ---"
(cd cmd/data-downloaders/download-wb-stocks && go run . --date $(date +%Y-%m-%d) --config ../../../$CONFIG_DIR/download-wb-stocks.yaml) || exit $?

# Phase 4: Supplies (incoming stock data for supply_incoming column)
echo "--- Supplies ---"
(cd cmd/data-downloaders/download-wb-supplies && go run . ${DAYS:+--days=$DAYS}) || exit $?

ELAPSED=$(( SECONDS - START ))
echo "=== Download completed in ${ELAPSED}s ==="
echo "Finished: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""
echo "Now run:"
cd /home/ilkoid/go-workspace/src/poncho-ai/cmd/data-analyzers/build-ma-sku-snapshots && go run .

