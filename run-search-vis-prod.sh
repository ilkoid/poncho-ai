#!/usr/bin/env bash
# run-search-vis-prod.sh — РЕАЛЬНЫЙ ручной прогон search-vis с параметрами
# ночного локального прогона (download-all.sh, фаза search-vis).
#
# Полный проход: все nmID по фильтру из локального PG (exclude_lengths /
# allowed_years / active_days), окно days 7, ЗАПИСЬ в wb_data_prod (upsert по
# snapshot_date — идемпотентно). В шторме занимает ~1-3 ч (15.10: 3ч2м);
# НЕ пересекать с ночным кроном 04:30 (двойной удар в одно ведро + дубль фазы).
# Запускается ВРУЧНУЮ пользователем.
#
# Запуск:  bash run-search-vis-prod.sh           — как ночью (полный проход)
#          bash run-search-vis-prod.sh --days 1  — доп.флаги утилиты проходят
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

LOG="$REPO/logs/search-vis-prod-$(date '+%Y-%m-%d-%H%M%S').log"
mkdir -p "$REPO/logs"

echo "=== РЕАЛЬНЫЙ прогон search-vis (запись в wb_data_prod, полный проход) | лог: $LOG ==="
go run "$REPO/cmd/data-downloaders/download-wb-search-vis-v2" \
    --config "$REPO/cmd/.configs/download-all/download-wb-search-vis-v2-PG.yaml" \
    --backend postgres \
    "$@" 2>&1 | tee "$LOG"
