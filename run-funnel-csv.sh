#!/bin/bash
# Ручной прогон воронки продаж (CSV-отчёт WB) на Mac — только PG, две фазы:
#   1) cards-v2     — обновить карточки (вдруг прибавились nm_id; воронка
#                     джойнится на cards)
#   2) funnel-csv-v2 — сам отчёт воронки (detail, 30 дней, wb_data_prod)
#
# Usage: bash run-funnel-csv.sh
# Квота WB (создание отчёта = 20/сутки на продавца). resume:true (в конфиге)
# ищет готовый отчёт того же периода в ЛОКАЛЬНОЙ БД ЭТОЙ машины (pkg/nmreport/
# downloader.go:49 — НЕ в API-списке): повторный прогон в тот же день = RESUMED
# без создания. Если записи нет — попытка создания (слот жив ~00:00–01:10 МСК
# и в любое время после починки внешнего дренажа; днём при живом дренаже — 429).
# ⚠️ НЕ запускать cards-фазу в окно ~00:05–00:20 МСК — пересечение с catalog-
# блоком VPS2 на том же ключе (02.11: обоюдные 429, cards VPS2 упал на page 8).
# Прогон до ~30 мин (poll отчёта) — под caffeinate: caffeinate -is bash run-funnel-csv.sh
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"

# go run требует cwd внутри модуля (скрипт могут звать абсолютным путём)
cd "$PONCHO"

# ── .env — ключи WB + PG (как в download-all.sh) ──
export PGHOST="${PGHOST:-localhost}"
export PGPORT="${PGPORT:-5432}"
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

echo "═══════  Funnel CSV (ручной, PG)  ═══════"
echo "  PG: ${PGHOST}:${PGPORT}/${PGDATABASE:-wb_data_prod}"
echo "═════════════════════════════════════════════"

# ── 1) Карточки — не фатально: цель прогона — воронка ──
echo "── Phase 1: Cards ──"
go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" \
  --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres \
  || echo "⚠️  cards упал (код $?) — продолжаю воронку" >&2

# ── 2) Воронка ──
echo "── Phase 2: Funnel CSV ──"
go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" \
  --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres
