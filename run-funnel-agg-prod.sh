#!/usr/bin/env bash
# run-funnel-agg-prod.sh — РЕАЛЬНЫЙ ручной прогон funnel-agg с прод-конфигом.
#
# Те же параметры, что ночной download-all (PG.yaml: окно 7 дней, бюджет 15м на
# страницу, ε 200мс), но с ЗАПИСЬЮ в wb_data_prod (upsert по nm_id+периоду —
# идемпотентно, повторный запуск безопасен). Запускается ВРУЧНУЮ пользователем:
# разовый дневной догон/репетиция ночной фазы.
#
# Запуск:  bash run-funnel-agg-prod.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

LOG="$REPO/logs/funnel-agg-prod-$(date '+%Y-%m-%d-%H%M%S').log"
mkdir -p "$REPO/logs"

echo "=== РЕАЛЬНЫЙ прогон (запись в wb_data_prod) | лог: $LOG ==="
go run "$REPO/cmd/data-downloaders/download-wb-funnel-agg-v2" \
    --config "$REPO/cmd/.configs/download-all/download-wb-funnel-agg-PG.yaml" \
    2>&1 | tee "$LOG"
