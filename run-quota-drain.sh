#!/bin/bash
# run-quota-drain.sh — раннер ВРЕМЕННОЙ кампании давления по квоте отчётов WB
# (20 созданий/сутки на аккаунт). Запускается кроном VPS2 в 00:10 (см. деплой
# в шапке cmd/ops-drain-report-quota/main.go): сама утилита ждёт, пока наш
# ночной блок создаст свои 3 отчёта (guard по GET-списку, дедлайн 01:05),
# и только потом закрывает остаток квоты наполнителями «Quota drain …».
#
# ВЫКЛЮЧАТЕЛЬ КАМПАНИИ — маркер-файл .quota-drain-enabled в корне репо:
#   включить:  touch .quota-drain-enabled
#   выключить: rm .quota-drain-enabled   (утилита и крон-строка остаются)
# Без маркера раннер молча выходит (exit 0) — крон можно не трогать.
#
# Usage: bash run-quota-drain.sh          (крон; репо-корень — см. cd ниже)
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
cd "$PONCHO"

mkdir -p "$PONCHO/logs"
CRONLOG="$PONCHO/logs/quota-drain-cron.log"

if [ ! -f "$PONCHO/.quota-drain-enabled" ]; then
  echo "$(date '+%F %T')  run-quota-drain: кампания ВЫКЛ (нет .quota-drain-enabled) — выходим" | tee -a "$CRONLOG"
  exit 0
fi

# .env — ключ WB_API_ANALYTICS_AND_PROMO_KEY (как в download-all*.sh)
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

echo "$(date '+%F %T')  run-quota-drain: кампания ВКЛ — запуск утилиты" | tee -a "$CRONLOG"
go run "$PONCHO/cmd/ops-drain-report-quota" \
  --config "$PONCHO/cmd/ops-drain-report-quota/config.yaml" \
  --apply 2>&1 | tee -a "$CRONLOG"
