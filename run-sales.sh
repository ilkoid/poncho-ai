#!/bin/bash
# Временный запускатор WB Sales Downloader v2 (только PG).
# Чинит период и подставляет .env так же, как download-all.sh.
#
# Usage:  bash run-sales.sh [BEGIN] [END]
#   без аргументов — 2026-07-09 → 2026-07-19 (с запасом, как просил)
# Пример: bash run-sales.sh 2026-07-12 2026-07-18
#
# После проверки утилиты скрипт можно удалить.
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
cd "$PONCHO"

# ── Загружаем .env (как в download-all.sh) ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Проверка WB_API_KEY: без него fallback на finance endpoint не сработает ──
if [ -z "${WB_API_KEY:-}" ]; then
  echo "WARN: WB_API_KEY пустой — 401 \"token scope not allowed\" повторится." >&2
  echo "      Добавь WB_API_KEY=... в $PONCHO/.env (ключ с широким scope из WB-кабинета)." >&2
fi

BEGIN="${1:-2026-07-09}"
END="${2:-2026-07-19}"

echo "═══════  WB Sales Downloader v2 (PG)  ═══════"
echo "  Period:  $BEGIN → $END"
echo "  PG:      ${PGHOST:-192.168.10.7}:${PGPORT:-15432}/${PGDATABASE:-wb_data_prod}"
echo "  Key:     WB_STAT(len=${#WB_STAT}) + WB_API_KEY(len=${#WB_API_KEY}) fallback"
echo "═════════════════════════════════════════════"

go run "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" \
  --config "$PONCHO/cmd/.configs/download-all/download-wb-sales-PG.yaml" \
  --backend postgres \
  --begin "$BEGIN" --end "$END"
