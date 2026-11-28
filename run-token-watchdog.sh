#!/bin/bash
# run-token-watchdog.sh — раннер сторожа срока жизни WB-токенов (JWT exp).
# Крон: Mac 09:00 / VPS2 09:10. Утилита читает .env и логи сама (read-only);
# письмо раннер шлёт ТОЛЬКО при ненулевом exit утилиты (тревога/конфиг-ошибка)
# через cmd/ops-notify/mail-notify. Тихий день = письма нет.
#
# Логирование: крон-редирект пишет logs/token-watchdog-cron.log (весь вывод),
# утилита дополнительно ведёт датированный evidence logs/token-watchdog-YYYY-MM-DD.log.
#
# КАНАЛ ПИСЕМ: SMTP-relay 10.120.11.31 доступен с Mac только через VPN
# (WireGuard; инцидент fbs-supplies 27.09 — «no route to host»). Гарантированный
# отправитель алертов — VPS2 (внутренняя сеть, канал проверен 19.11); Mac-письмо
# идёт как избыточное при живом VPN. При недоступности relay раннер честно
# пишет WARNING и сохраняет exit-код — тревога видна в cron-логе в любом случае.
#
# Usage: bash run-token-watchdog.sh
set -euo pipefail

PONCHO="$(cd "$(dirname "$0")" && pwd)"
cd "$PONCHO"

# PATH для cron: у крона нет окружения юзера. Mac: go в /opt/homebrew/bin
# (идиома fill-dims.sh:26, run-download-all-daily.sh:20); VPS2: /usr/local/go/bin
# (идиома run-quota-drain.sh:25-26). Инцидент 20–27.11: без homebrew-пути Mac-крон
# падал «FAIL: go не найден» 8 дней подряд — сторож на маке был слеп, спасала
# только VPS2-нога (токены те же, канал писем гарантирован).
export PATH="/opt/homebrew/bin:$PATH:/usr/local/go/bin:${HOME}/go/bin"
command -v go >/dev/null 2>&1 || { echo "FAIL: go не найден в PATH (cron без профиля?)" >&2; exit 1; }

mkdir -p "$PONCHO/logs"

# .env — нужен SMTP_PASSWORD для письма (утилита читает .env сама, напрямую)
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

RC=0
OUT="$(go run "$PONCHO/cmd/ops-token-watchdog" \
  --config "$PONCHO/cmd/ops-token-watchdog/config.yaml" 2>&1)" || RC=$?
echo "$OUT"

if [ "$RC" -eq 0 ]; then
  exit 0
fi

VERDICT="$(printf '%s\n' "$OUT" | grep -m1 'VERDICT:' || echo 'см. лог')"
HOST_TAG="$(hostname -s)"

if [ -n "${SMTP_PASSWORD:-}" ]; then
  printf '%s\n' "$OUT" | go run "$PONCHO/cmd/ops-notify/mail-notify" \
    --config "$PONCHO/cmd/ops-notify/mail-notify/config-token-watchdog.yaml" \
    --subject "⚠️ token-watchdog ${HOST_TAG}: ${VERDICT}" \
    || echo "WARNING: письмо не ушло (ошибка mail-notify выше)" >&2
else
  echo "WARNING: SMTP_PASSWORD пуст — письмо не отправлено" >&2
fi
exit "$RC"
