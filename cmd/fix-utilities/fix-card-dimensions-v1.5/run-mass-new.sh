#!/usr/bin/env bash
# run-mass-new.sh — массовый --apply: заполнение нулевых габаритов из 1С.
#
# Кандидаты: все карточки с L=W=H=0 и замерами в 1С (пин не нужен, фильтр only_new).
# Документы и kizMarked уезжают round-trip'ом (проверено двумя живыми тестами 12.10).
# Внутри apply: стоп на первой ошибке валидации WB, статусы в fix_card_dimensions_staging.
#
# Перед запуском желательно: bash prepare-fix-dims.sh (свежий кеш; гард 24ч и так не пустит по протухшему).

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$DIR/../../.." && pwd)"
CFG="$DIR/${1:-config-mass-new.yaml}"

set -a; source "$REPO/.env"; set +a
if [[ -z "${WB_API_ANALYTICS_AND_PROMO_KEY:-}" && -z "${WB_API_KEY:-}" ]]; then
    echo "ERROR: в .env нет WB_API_ANALYTICS_AND_PROMO_KEY / WB_API_KEY" >&2
    exit 1
fi

cd "$REPO"
go run "$DIR" --stage --config "$CFG"
go run "$DIR" --diff  --config "$CFG"

echo ""
echo ">>> Через 10 секунд — РЕАЛЬНАЯ запись в WB (все staged карточки). Ctrl+C для отмены."
sleep 10
go run "$DIR" --apply --config "$CFG"

echo ""
echo "Статусы: sqlite3 ~/dev/fix-dims.db \"SELECT status, COUNT(*) FROM fix_card_dimensions_staging GROUP BY status\""
