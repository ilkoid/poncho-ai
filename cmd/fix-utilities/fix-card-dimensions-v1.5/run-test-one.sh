#!/usr/bin/env bash
# run-test-one.sh — реальный --apply на одном тестовом артикуле.
#
# Артикул по умолчанию: 22621252 (пин в config-test-one.yaml).
# Допник:               bash run-test-one.sh config-test-two.yaml
#                       (32621411: смешанный сертификат+декларация, kizMarked=false)
#
# Габариты из 1С (+padding 2 см), документы и kizMarked уезжают round-trip'ом.
# Гард свежести кеша (24 ч) внутри утилиты; повторный запуск идемпотентен.

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$DIR/../../.." && pwd)"
CONFIG="${1:-config-test-one.yaml}"
CFG="$DIR/$CONFIG"

set -a; source "$REPO/.env"; set +a
if [[ -z "${WB_API_ANALYTICS_AND_PROMO_KEY:-}" && -z "${WB_API_KEY:-}" ]]; then
    echo "ERROR: в .env нет WB_API_ANALYTICS_AND_PROMO_KEY / WB_API_KEY" >&2
    exit 1
fi

cd "$REPO"
go run "$DIR" --stage --config "$CFG"
go run "$DIR" --diff  --config "$CFG"

echo ""
echo ">>> Через 5 секунд — реальная запись в WB ($CONFIG, 1 карточка). Ctrl+C для отмены."
sleep 5
go run "$DIR" --apply --config "$CFG"
