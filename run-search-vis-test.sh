#!/usr/bin/env bash
# run-search-vis-test.sh — дневная проверка схемы ретраев search-vis
# («подсказка + ε», повтор без локального пейсинга).
#
# Что делает: реальный WB API (ключи из .env), НОЛЬ записей (--dry-run) —
# прогон по нескольким nmID (аргумент или дефолт — топ продаж недели), лог
# дублируется на экран и в файл. Прерывать — Ctrl+C, данные не пострадают.
#
# Смотреть в логе: шапку «Retry: hint+eps 200ms · retries 10 · rescue 30m»;
# кулдауны «N.2s (server: Ns)» (N.2 = подсказка + ε 200мс); интервалы
# повторов = подсказка+ε БЕЗ добавок лимитера; в конце — «rescue» если
# что-то упало (в dry-run долив тоже идёт, записи нет).
#
# Запуск:  bash run-search-vis-test.sh                 — дефолтные 5 nmID
#          bash run-search-vis-test.sh 12345678,87654321 — свои nmID
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

# Дефолт — топ-5 артикулов недели по продажам (чтобы прогон был коротким:
# ~5 продуктов × лимит запросов — минуты, а не часы полного прохода).
NM_IDS="${1:-38930359,176706266,13982573,13986610,176706283}"

LOG="$REPO/logs/search-vis-test-$(date '+%Y-%m-%d-%H%M%S').log"
mkdir -p "$REPO/logs"

echo "=== тест search-vis (dry-run): nmID=$NM_IDS | лог: $LOG ==="
go run "$REPO/cmd/data-downloaders/download-wb-search-vis-v2" \
    --config "$REPO/cmd/.configs/download-all/download-wb-search-vis-v2-PG.yaml" \
    --backend postgres --dry-run --nm-ids "$NM_IDS" 2>&1 | tee "$LOG"
