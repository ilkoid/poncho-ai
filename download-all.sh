#!/bin/bash
# Simplified WB data downloader
# All settings from config.yaml, stops on first error

# Change to script directory (project root)
cd "$(dirname "$0")"

echo "Starting WB data download..."

# Phase 1: Quick updates
# (cd cmd/data-downloaders/download-wb-sales && go run .) || exit $?
# (cd cmd/data-downloaders/download-wb-feedbacks && go run .) || exit $?

# Phase 2: Medium downloads
# (cd cmd/data-downloaders/download-wb-stocks && go run .) || exit $?

# Phase 3: Long downloads
# (cd cmd/data-downloaders/download-wb-stock-history && go run .) || exit $?
(cd cmd/data-downloaders/download-wb-funnel && go run .) || exit $?
# (cd cmd/data-downloaders/download-wb-funnel-agg && go run .) || exit $?

# Phase 4: Campaign data
(cd cmd/data-downloaders/download-wb-promotion && go run .) || exit $?

echo "All downloads completed successfully"
