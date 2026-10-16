#!/usr/bin/env bash
# run-funnel-agg-test.sh — проверка схемы ретраев funnel-agg («подсказка + ε»).
#
# Что делает: 1 день данных, --dry-run (НЕТ записей в БД — только SELECT-оценка
# тотала из локального PG для [N/M % ETA]), лог дублируется на экран и в файл.
# В шторме закончится за ~3-5 мин честным «page budget exhausted» (exit 1),
# в тишине — страницами и «done» (exit 0).
#
# Запуск:  bash run-funnel-agg-test.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

LOG="$REPO/logs/funnel-agg-test-$(date '+%Y-%m-%d-%H%M%S').log"
mkdir -p "$REPO/logs"

echo "=== лог: $LOG ==="
go run "$REPO/cmd/data-downloaders/download-wb-funnel-agg-v2" \
    --config "$REPO/cmd/.configs/download-all/download-wb-funnel-agg-TEST.yaml" \
    --dry-run 2>&1 | tee "$LOG"
