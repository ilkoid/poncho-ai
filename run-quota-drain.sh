#!/bin/bash
# run-quota-drain.sh — раннер ВРЕМЕННОЙ кампании давления по квоте отчётов WB
# (20 созданий/сутки на аккаунт). Запускается кроном VPS2 в 00:10: сама
# утилита ждёт, пока наш ночной блок создаст свои 3 отчёта (guard по
# GET-списку, дедлайн 01:05), и только потом закрывает остаток квоты
# наполнителями «Quota drain …».
#
# ВЫКЛЮЧАТЕЛЬ КАМПАНИИ — маркер-файл .quota-drain-enabled в корне репо:
#   включить:  touch .quota-drain-enabled
#   выключить: rm .quota-drain-enabled   (утилита и крон-строка остаются)
# Без маркера раннер молча выходит (exit 0) — крон можно не трогать.
#
# Логирование: крон-редирект пишет logs/quota-drain-cron.log (весь вывод),
# утилита дополнительно ведёт датированный evidence logs/quota-drain-YYYY-MM-DD.log.
#
# Usage: bash run-quota-drain.sh          (крон; репо-корень — см. cd ниже)
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
cd "$PONCHO"

# PATH для cron: у крона нет окружения юзера (ночь 04.11: «go: command not
# found» на строке go run — дренаж не выстрелил, пакет нарушителя прошёл
# целиком). Идиома download-all-v2.sh:33-34.
export PATH="$PATH:/usr/local/go/bin:${HOME}/go/bin"
command -v go >/dev/null 2>&1 || { echo "FAIL: go не найден в PATH (cron без профиля?) — допиши путь в PATH-строку скрипта" >&2; exit 1; }

mkdir -p "$PONCHO/logs"

if [ ! -f "$PONCHO/.quota-drain-enabled" ]; then
  echo "$(date '+%F %T')  run-quota-drain: кампания ВЫКЛ (нет .quota-drain-enabled) — выходим"
  exit 0
fi

# .env — ключ WB_API_ANALYTICS_AND_PROMO_KEY (как в download-all*.sh)
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

echo "$(date '+%F %T')  run-quota-drain: кампания ВКЛ — запуск утилиты"
go run "$PONCHO/cmd/ops-drain-report-quota" \
  --config "$PONCHO/cmd/ops-drain-report-quota/config.yaml" \
  --apply
