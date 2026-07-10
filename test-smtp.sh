#!/usr/bin/env bash
#
# test-smtp.sh — дымовой тест SMTP-сервера (10.120.11.31:465).
# Отправляет ОДНО письмо с CSV-вложением через curl (smtps, неявный TLS).
# Это тестовый стенд для проверки связки «сервер + креды», НЕ production-отправщик.
# Production-отправка идёт через пакет pkg/email (Go).
#
# Параметры шифрования намеренно повторяют конфиг сисадминов:
#   smtps://   = неявный TLS на 465 (= "tls on")
#   -k         = НЕ проверять сертификат сервера (= "tls_certcheck off")
#
# Usage:
#   ./test-smtp.sh <smtp-пароль> [получатель]
#   ./test-smtp.sh 'p@ss' kashparov.i@playtoday.ru
#
# ВНИМАНИЕ (безопасность):
#   - Пароль передаётся аргументом → виден в `ps aux` другим юзерам VPS, пока
#     выполняется curl, и сохраняется в истории bash. Это допустимо для
#     одноразового теста. После проверки удалите скрипт и почистите историю.
#   - Флаг -v печатает SMTP-диалог: команда AUTH содержит base64(логин\0пароль),
#     который легко декодируется. Не вставляйте вывод -v в чаты/тикеты.
#
set -euo pipefail

# --- конфиг сервера (от сисадминов) ---
HOST="10.120.11.31"
PORT="465"
FROM_ADDR="ai-tools@playtoday.ru"          # from:  обратный адрес в письме
SMTP_USER="it_service@playtoday.ru"         # user:  логин для SMTP AUTH

# --- аргументы ---
PASSWORD="${1:-}"
TO="${2:-kashparov.i@playtoday.ru}"

if [ -z "$PASSWORD" ]; then
    echo "Usage: $0 <smtp-пароль> [получатель]" >&2
    echo "  пароль — значение password из конфига сисадминов" >&2
    echo "  получатель — по умолчанию $TO" >&2
    exit 1
fi

# проверяем, что curl есть и умеет smtps
if ! curl --version 2>/dev/null | grep -qi 'smtps'; then
    echo "ОШИБКА: curl не поддерживает smtps. Нужен curl, собранный с SMTP(S)." >&2
    exit 1
fi

# --- тестовое вложение (CSV с кириллицей) генерируем на лету ---
CSV=$'metric,value\r\nrows,3\r\nnote,тест csv из скрипта\r\n'
ATT_B64=$(printf '%s' "$CSV" | base64)        # по умолчанию перенос строк по 76 — RFC-совместимо

# --- тема письма: RFC 2047 (кириллица → base64-encoding) ---
SUBJ_B64=$(printf '%s' "Тест SMTP pkg/email (curl, smtps://465)" | base64 -w0)
SUBJECT="=?utf-8?b?${SUBJ_B64}?="

# --- граница multipart ---
BOUNDARY="poncho-test-$(date +%s)-$$"

# --- собираем MIME: multipart/mixed = текстовая часть + вложение ---
MSG=$(cat <<EOF
From: ${FROM_ADDR}
To: ${TO}
Subject: ${SUBJECT}
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="${BOUNDARY}"

--${BOUNDARY}
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: 8bit

Это тестовое письмо, отправленное скриптом test-smtp.sh через curl.
Сервер: ${HOST}:${PORT} (smtps, implicit TLS).
Вложение: test.csv.

--${BOUNDARY}
Content-Type: text/csv; charset=utf-8
Content-Disposition: attachment; filename="test.csv"
Content-Transfer-Encoding: base64

${ATT_B64}
--${BOUNDARY}--
EOF
)

echo ">>> Отправка письма с вложением на ${TO}"
echo ">>> Сервер: smtps://${HOST}:${PORT} (TLS сертификат НЕ проверяется, как в конфиге)"
echo ">>> LOGIN: ${SMTP_USER}"
echo

# --- отправка ---
# -k                 = не проверять сертификат (tls_certcheck off)
# smtps://           = неявный TLS на 465 (НЕ STARTTLS)
# --mail-from/rcpt   = конверт SMTP (MAIL FROM / RCPT TO)
# --user             = SMTP AUTH (PLAIN, по защищённому TLS-каналу)
# sed 's/$/\r/'      = LF → CRLF, как требует RFC 5321
printf '%s\n' "$MSG" | sed 's/$/\r/' | curl \
    -k \
    --connect-timeout 10 \
    --max-time 30 \
    -v \
    --url "smtps://${HOST}:${PORT}" \
    --mail-from "${FROM_ADDR}" \
    --mail-rcpt "${TO}" \
    --user "${SMTP_USER}:${PASSWORD}" \
    -T -

echo
echo ">>> curl exit: $?"
echo ">>> Если письмо пришло — сервер + креды + TLS работают."
echo ">>> Проверьте папку «Спам», если письма нет во Входящих."
