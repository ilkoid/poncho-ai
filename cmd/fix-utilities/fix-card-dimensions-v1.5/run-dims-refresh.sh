#!/usr/bin/env bash
# run-dims-refresh.sh — свежий кеш + stage + diff. БЕЗ apply/dry-run (для cron).
#
# Что делает:
#   1. prepare-fix-dims.sh   — обновляет кеш ~/dev/fix-dims.db (1С + cards + stocks)
#   2. --stage               — staging: карточки с нулевыми габаритами по конфигу
#   3. --diff                — before/after по staging
# Запись в WB НЕ выполняется — apply всегда вручную: bash run-mass-new.sh
#
# Usage:  bash run-dims-refresh.sh [конфиг]     (default: config-years-25-26.yaml)
# Cron (пример, nightly 04:50):  50 4 * * * bash /Users/ilkoid/dev/poncho-ai/cmd/fix-utilities/fix-card-dimensions-v1.5/run-dims-refresh.sh >> /Users/ilkoid/dev/poncho-ai/logs/cron-dims-refresh.log 2>&1

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$DIR/../../.." && pwd)"
CFG="$DIR/${1:-config-years-25-26.yaml}"

bash "$REPO/prepare-fix-dims.sh"

cd "$REPO"
go run "$DIR" --stage --config "$CFG"
go run "$DIR" --diff  --config "$CFG"
