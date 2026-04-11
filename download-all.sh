#!/bin/bash
# WB + 1C full data refresh script
# All utilities sequential (shared wb-sales.db)
# Organized by speed: fast → slow → internal
#
# Usage: bash download-all.sh [days]
#   days  - override --days for utilities that support it (default: from config.yaml)

cd "$(dirname "$0")"

# Optional: override days (default from each config.yaml)
DAYS="${1:-}"

echo "=== WB + 1C Data Download ==="
echo "Started: $(date '+%Y-%m-%d %H:%M:%S')"
START=$SECONDS

# Phase 1: Fast (~2-5 min) — high rate limits (100-180 req/min)
echo "--- Phase 1: Fast (feedbacks, cards, prices) ---"
(cd cmd/data-downloaders/download-wb-feedbacks && go run . ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-cards && go run .) || exit $?
(cd cmd/data-downloaders/download-wb-prices && go run .) || exit $?

# Phase 2: Moderate (~5-10 min) — mixed rate limits
echo "--- Phase 2: Moderate (promotion, region-sales) ---"
(cd cmd/data-downloaders/download-wb-promotion && go run . ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-region-sales && go run . ${DAYS:+--days=$DAYS}) || exit $?

# Phase 3: Slow (~20-40 min) — Analytics API (3 req/min), Statistics API (1 req/min)
echo "--- Phase 3: Slow — Analytics API (funnel, funnel-agg, sales) ---"
(cd cmd/data-downloaders/download-wb-funnel && go run .) || exit $?
(cd cmd/data-downloaders/download-wb-funnel-agg && go run .) || exit $?
(cd cmd/data-downloaders/download-wb-sales && go run . ${DAYS:+--days=$DAYS}) || exit $?

# Phase 4: Async/Slow (~15-30 min) — stock data with async report generation
echo "--- Phase 4: Async/Slow (stock-history, stocks) ---"
(cd cmd/data-downloaders/download-wb-stock-history && go run . ${DAYS:+--days=$DAYS}) || exit $?
(cd cmd/data-downloaders/download-wb-stocks && go run .) || exit $?

# Phase 5: Internal APIs (~5 min) — 1C/PIM, no WB rate limits
echo "--- Phase 5: Internal (1C/PIM catalog) ---"
(cd cmd/data-downloaders/download-1c-data && go run .) || exit $?

ELAPSED=$(( SECONDS - START ))
echo "=== All downloads completed in ${ELAPSED}s ==="
echo "Finished: $(date '+%Y-%m-%d %H:%M:%S')"
