#!/usr/bin/env bash
# run-funnel-agg-test.sh — проверка схемы ретраев funnel-agg («подсказка + ε»).
#
# Что делает: --dry-run (НЕТ записей в БД — только реальный API и SELECT-оценка
# тотала/offset-resume из локального PG для [N/M % ETA]), лог дублируется на
# экран и в файл.
#
# Запуск:  bash run-funnel-agg-test.sh        — TEST-конфиг: 1 день, бюджет 3м/стр
#          bash run-funnel-agg-test.sh PG     — прод-конфиг: 7 дней, бюджет 15м/стр
#                                               (те же параметры, что ночной прогон;
#                                               в шторме ~14-21 стр, ориентир 10-25 мин)
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

ARG="${1:-TEST}"
case "$ARG" in
  TEST) CFG="$REPO/cmd/.configs/download-all/download-wb-funnel-agg-TEST.yaml" ;;
  PG)   CFG="$REPO/cmd/.configs/download-all/download-wb-funnel-agg-PG.yaml" ;;
  *)    CFG="$ARG" ;; # явный путь к yaml
esac

TAG="$(basename "$CFG" .yaml)"; TAG="${TAG#download-wb-funnel-agg-}"
LOG="$REPO/logs/funnel-agg-test-$TAG-$(date '+%Y-%m-%d-%H%M%S').log"
mkdir -p "$REPO/logs"

echo "=== лог: $LOG ==="
go run "$REPO/cmd/data-downloaders/download-wb-funnel-agg-v2" \
    --config "$CFG" \
    --dry-run 2>&1 | tee "$LOG"
