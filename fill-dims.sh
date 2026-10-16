#!/usr/bin/env bash
# fill-dims.sh — заполнение НУЛЕВЫХ габаритов WB из 1С: карточки → 1С → stage → diff → apply.
#
# Порядок (по требованию): сначала свежие карточки WB (documents/kizMarked для
# round-trip — максимально близко к моменту записи), затем свежие замеры 1С,
# затем staging/diff и реальный apply. Остатки (stocks) не качаем — конфиг
# с in_stock: false; если смените на true — верните фазу stocks из prepare-fix-dims.sh.
#
# Охват: артикулы 2025-2026 (config-years-25-26.yaml), только карточки с L=W=H=0.
# Предохранители: гард свежести кеша 24ч (apply откажет по протухшему кешу),
# only_new, стоп на первой ошибке валидации WB, лок от параллельных прогонов.
# Повторный запуск идемпотентен.
#
# Usage:  bash fill-dims.sh [конфиг]     (default: config-years-25-26.yaml)
# Cron:   30 5 * * * bash /Users/ilkoid/dev/poncho-ai/fill-dims.sh >> /Users/ilkoid/dev/poncho-ai/logs/cron-fill-dims.log 2>&1

set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
FIX="$REPO/cmd/fix-utilities/fix-card-dimensions-v1.5"
CFG="$FIX/${1:-config-years-25-26.yaml}"
DB="$HOME/dev/fix-dims.db"
STATUS="$REPO/logs/fill-dims-last-run.txt"

# cron PATH минимален (/usr/bin:/bin) — добавляем homebrew (go), как в run-download-all-daily.sh
export PATH="/opt/homebrew/bin:$PATH"

# Лок: не пускать второй прогон, пока живёт первый
LOCK="$REPO/.fill-dims.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
    echo "$(date '+%F %T') ERROR: уже запущено (лок $LOCK). Если прошлый прогон точно завершён: rmdir $LOCK" >&2
    exit 1
fi

# Статус последнего прогона: OK/FAILED + время (ночными провалами можно поймать одним взглядом)
finish() {
    local rc=$?
    rmdir "$LOCK" 2>/dev/null || true
    if [[ $rc -eq 0 ]]; then
        echo "$(date '+%F %T') OK" > "$STATUS"
    else
        echo "$(date '+%F %T') FAILED rc=$rc" >> "$STATUS"
    fi
}
trap finish EXIT

set -a; source "$REPO/.env"; set +a
if [[ -z "${WB_API_ANALYTICS_AND_PROMO_KEY:-}" && -z "${WB_API_KEY:-}" ]]; then
    echo "$(date '+%F %T') ERROR: в .env нет WB_API_ANALYTICS_AND_PROMO_KEY / WB_API_KEY" >&2
    exit 1
fi
for var in WB_API_CONTENT_KEY ONEC_API_URL ONEC_PIM_URL; do
    if [[ -z "${!var:-}" ]]; then
        echo "$(date '+%F %T') ERROR: в .env нет $var" >&2
        exit 1
    fi
done

# Пре-флайт доступности WB (гатч 12.10: VPN резал все *.wildberries.ru таймаутами).
# 000 = хост недоступен → fail fast с внятной причиной, без ночного цикла ретраев.
WB_CODE="$(curl -s --max-time 10 -o /dev/null -w '%{http_code}' https://content-api.wildberries.ru || true)"
if [[ "$WB_CODE" == "000" ]]; then
    echo "$(date '+%F %T') ERROR: content-api.wildberries.ru недоступен (http 000, таймаут) — проверить VPN/сеть" >&2
    exit 1
fi

cd "$REPO"
echo "=== $(date '+%F %T') fill-dims: конфиг $(basename "$CFG"), кеш $DB ==="

# 1. Свежие карточки WB (~7 мин, только чтение API)
echo "=== $(date '+%F %T') фаза 1: карточки WB ==="
go run "$REPO/cmd/data-downloaders/download-wb-cards-v2" \
    --config "$REPO/cmd/data-downloaders/download-wb-cards-v2/config-fixdims.yaml" \
    --backend sqlite --db "$DB"

# 2. Свежие данные 1С (товары + замеры, ~20 сек)
echo "=== $(date '+%F %T') фаза 2: 1С ==="
go run "$REPO/cmd/data-downloaders/download-1c-data-v2" \
    --config "$REPO/cmd/data-downloaders/download-1c-data-v2/config-fixdims.yaml" \
    --backend sqlite --db "$DB"

# 3-4. Staging + diff (локально)
echo "=== $(date '+%F %T') stage ==="
go run "$FIX" --stage --config "$CFG"
echo "=== $(date '+%F %T') diff ==="
go run "$FIX" --diff --config "$CFG"

# 5. APPLY — реальная запись в WB (стоп при первой ошибке валидации)
echo "=== $(date '+%F %T') apply (запись в WB) ==="
go run "$FIX" --apply --config "$CFG"

# 6. Итог
echo "=== $(date '+%F %T') статусы staging ==="
if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$DB" "SELECT status, COUNT(*) FROM fix_card_dimensions_staging GROUP BY status;"
fi
echo "=== $(date '+%F %T') fill-dims: готово ==="
