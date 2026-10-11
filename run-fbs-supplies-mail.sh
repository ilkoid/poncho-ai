#!/bin/bash
# run-fbs-supplies-mail.sh — конвейер рассылки SLA-дашборда FBS-поставок:
#   1) обновить данные (download-wb-fbs-orders-v2 — единственный источник
#      таблиц дашборда: fbs_supplies / fbs_orders / fbs_orders_status_log;
#      пишет в wb_data_prod по cmd/.configs/download-all/download-wb-fbs-orders-PG.yaml)
#   2) собрать дашборд (fbs-supplies-report → reports/fbs-supplies-<дата>.html)
#   3) доставить двумя каналами (каждый со своим статусом; exit 1 только если
#      не доставлено НИКУДА — для cron это «прогон бесполезен»):
#      • Telegram sendDocument — файл + caption-сводка; креды TG_BOT_TOKEN/
#        TG_CHAT_ID в .env (тот же бот/чат, что у ночника); TG_PROXY опционален
#      • mail-notify --attach — вложение + текстовое тело; получатели —
#        recipients.to в cmd/ops-notify/mail-notify/config-fbs-supplies.yaml;
#        SMTP_PASSWORD в .env. Сети без роута к relay 10.120.11.31 (например,
#        домашняя) канал просто честно падает — Telegram остаётся рабочим.
#
# Usage: bash run-fbs-supplies-mail.sh [--test] [--no-refresh]
#   --test        почта: ТОЛЬКО kashparov.i@playtoday.ru (флаг --to), тема с [TEST];
#                 Telegram: тот же чат, caption с [TEST]
#   --no-refresh  пропустить фазу загрузки (быстрая пересборка/переотправка)
#
# Переменные окружения (можно переопределять при запуске):
#   ORDERS_DAYS=3   окно фазы заданий (свежий supply_id)
#   STATUS_DAYS=2   окно фазы статусов (незакрытые задания качаются всегда)
#   FEED_DAYS=2     глубина ленты; держи ≥ интервала между прогонами
#   TG_PROXY=...    прокси для api.telegram.org (socks5h://…) — нужен не везде
#
# Cron-совместимость: скрипт не рассчитывает на интерактивный шелл — сам
# пополняет PATH (go), дозагружает $PONCHO/.env, пишет лог в logs/, защищён
# локами от параллельного прогона, exit 1 при любой упавшей фазе.
# Образец crontab-строки (после проверки — по рабочим дням к 9:00 МСК):
#   0 9 * * 1-5  /bin/bash /Users/ilkoid/dev/poncho-ai/run-fbs-supplies-mail.sh >> /Users/ilkoid/dev/poncho-ai/logs/cron.log 2>&1

# ── Args ──
TEST_MODE=0
NO_REFRESH=0
for arg in "$@"; do
  case "$arg" in
    --test)       TEST_MODE=1 ;;
    --no-refresh) NO_REFRESH=1 ;;
    *) echo "Неизвестный аргумент: $arg (допустимо: --test, --no-refresh)" >&2; exit 2 ;;
  esac
done
TEST_TO="kashparov.i@playtoday.ru"

# ── Окружение: cron видит урезанный PATH — дополним типовыми локациями go ──
export PATH="$PATH:/usr/local/go/bin:/opt/homebrew/bin:${HOME}/go/bin"
export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
MAIL_CFG="$PONCHO/cmd/ops-notify/mail-notify/config-fbs-supplies.yaml"
REPORT_HTML="$PONCHO/reports/fbs-supplies-$(date +%F).html"

ORDERS_DAYS="${ORDERS_DAYS:-3}"
STATUS_DAYS="${STATUS_DAYS:-2}"
FEED_DAYS="${FEED_DAYS:-2}"

# ── Лог: всё (stdout+stderr) дублируется в файл ──
mkdir -p "$PONCHO/logs" "$PONCHO/reports"
LOGFILE="$PONCHO/logs/fbs-supplies-mail-$(date +%F).log"
exec > >(tee -a "$LOGFILE") 2>&1
log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }
find "$PONCHO/logs" -name 'fbs-supplies-mail-*.log' -mtime +30 -delete 2>/dev/null || true

# ── .env — канонический источник секретов ──
if [ ! -f "$PONCHO/.env" ]; then
  echo "FAIL: нет $PONCHO/.env (PG_PWD, WB_API_KEY/WB_API_CONTENT_KEY, SMTP_PASSWORD)" >&2
  exit 1
fi
set -a
# shellcheck disable=SC1091 # путь динамический; секреты в lint не тащим
. "$PONCHO/.env"
set +a

# ── Fail fast: обязательные переменные и go ──
MISSING=()
[ -z "${PG_PWD:-}" ] && MISSING+=("PG_PWD")
[ -z "${WB_API_KEY:-}" ] && [ -z "${WB_API_CONTENT_KEY:-}" ] && MISSING+=("WB_API_KEY|WB_API_CONTENT_KEY")
if [ "${#MISSING[@]}" -gt 0 ]; then
  echo "FAIL: в .env нет: ${MISSING[*]}" >&2
  exit 1
fi
# Каналы доставки: нужен хотя бы один (иначе рассылать нечем).
MAIL_READY=0; TG_READY=0
[ -n "${SMTP_PASSWORD:-}" ] && MAIL_READY=1
[ -n "${TG_BOT_TOKEN:-}" ] && [ -n "${TG_CHAT_ID:-}" ] && TG_READY=1
if [ "$MAIL_READY" = 0 ] && [ "$TG_READY" = 0 ]; then
  echo "FAIL: нет ни одного канала доставки: .env должен содержать SMTP_PASSWORD и/или TG_BOT_TOKEN+TG_CHAT_ID" >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "FAIL: go не найден в PATH (cron? добавь локацию go в PATH внутри скрипта)" >&2
  exit 1
fi

# ── Lock: свой + не пересекаться с ночным download-all / fetch-fbs-acceptance
#     (двойные записи в те же прод-таблицы) ──
for other in .download-all.lock .fetch-fbs-acceptance.lock; do
  if [ -d "$PONCHO/$other" ]; then
    log "SKIP: идёт $other — данные обновит уже запущенный прогон"
    exit 0
  fi
done
LOCKDIR="$PONCHO/.fbs-supplies-mail.lock"
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  log "SKIP: другой run-fbs-supplies-mail уже идёт (lock: $LOCKDIR)"
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast: PG доступен ──
if ! nc -z -w 5 "$PGHOST" "$PGPORT" 2>/dev/null; then
  echo "FAIL: PostgreSQL $PGHOST:$PGPORT недоступен (PGHOST/PGPORT/сеть?)" >&2
  exit 1
fi

cd "$PONCHO" || exit 1 # go run требует корня модуля

FAILED=()
RUN_COUNT=0
run() { # run <cmd…> — fail-tracking обёртка; имя утилиты = basename каталога из $3
  local name; name="$(basename "$3")"
  RUN_COUNT=$(( RUN_COUNT + 1 ))
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    echo "⚠️  FAIL: $name (exit $rc)" >&2
  fi
}

TODAY="$(date +%F)"
SUBJECT="FBS: SLA поставок на СЦ — $TODAY"
[ "$TEST_MODE" = 1 ] && SUBJECT="[TEST] $SUBJECT"

# tg_send_file <path> <caption> — sendDocument (файл + подпись ≤1024 симв).
# Паттерн как tg_send() в download-all-v2.sh: curl, "ok":true в ответе,
# креды TG_BOT_TOKEN/TG_CHAT_ID в .env; TG_PROXY (socks5h://…) опционален.
# 0 = доставлено, 1 = нет (прогон не роняет — статус уходит в Summary).
tg_send_file() {
  local path="$1" caption="$2" resp
  if [ -z "${TG_BOT_TOKEN:-}" ] || [ -z "${TG_CHAT_ID:-}" ]; then
    log "WARNING: TG_BOT_TOKEN/TG_CHAT_ID не заданы в .env — Telegram-канал пропущен"
    return 1
  fi
  local proxy_args=()
  if [ -n "${TG_PROXY:-}" ]; then
    proxy_args=(--proxy "${TG_PROXY}")
  fi
  resp=$(curl -s --connect-timeout 10 --max-time 120 "${proxy_args[@]}" \
    "https://api.telegram.org/bot${TG_BOT_TOKEN}/sendDocument" \
    --form "chat_id=${TG_CHAT_ID}" \
    --form "caption=${caption}" \
    --form "document=@${path}")
  if printf '%s' "$resp" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    return 0
  fi
  log "WARNING: Telegram отклонил отправку: ${resp:0:300}"
  return 1
}

START=$SECONDS
log "═══════ Рассылка SLA-дашборда FBS-поставок ═══════"
log "Режим: $([ "$TEST_MODE" = 1 ] && echo "TEST (почта: только $TEST_TO; TG: рабочий чат)" || echo "prod") · каналы: Telegram=$([ "$TG_READY" = 1 ] && echo "да" || echo "нет"), почта=$([ "$MAIL_READY" = 1 ] && echo "да" || echo "нет")${NO_REFRESH:+ · без обновления данных}"

# ── Фаза 1/3: обновление данных (поставки + статусы + лента) ──
if [ "$NO_REFRESH" = 1 ]; then
  log "── 1/3: пропуск (--no-refresh) ──"
else
  log "── 1/3: download-wb-fbs-orders-v2 (задания ${ORDERS_DAYS}д · статусы ${STATUS_DAYS}д · лента ${FEED_DAYS}д) ──"
  run go run "$PONCHO/cmd/data-downloaders/download-wb-fbs-orders-v2" \
    --config "$C/download-wb-fbs-orders-PG.yaml" \
    --backend postgres \
    --days "$ORDERS_DAYS" \
    --status-window-days "$STATUS_DAYS" \
    --feed-days "$FEED_DAYS"
fi

# ── Фаза 2/3: сборка HTML-дашборда ──
log "── 2/3: fbs-supplies-report → $REPORT_HTML ──"
RUN_COUNT=$(( RUN_COUNT + 1 ))
BUILD_OUT="$(mktemp /tmp/fbs-supplies-build.XXXXXX)"
go run "$PONCHO/cmd/data-analyzers/fbs-supplies-report" \
  --config "$PONCHO/cmd/data-analyzers/fbs-supplies-report/config.yaml" \
  --html "$REPORT_HTML" \
  2>&1 | tee "$BUILD_OUT"
BUILD_RC=${PIPESTATUS[0]}
if [ "$BUILD_RC" -ne 0 ] || [ ! -s "$REPORT_HTML" ]; then
  FAILED+=("fbs-supplies-report (exit $BUILD_RC)")
  echo "⚠️  FAIL: дашборд не собран (exit $BUILD_RC, файл: ${REPORT_HTML:-нет})" >&2
fi

# ── Фаза 3/3: доставка (Telegram + почта; только если дашборд собран) ──
MAIL_OK=0; TG_OK=0
if [ -s "$REPORT_HTML" ]; then
  SUMMARY_TXT="$(sed -n '/── СВОДКА ──/,/Готово за/p' "$BUILD_OUT")"
  SUMMARY_TXT="${SUMMARY_TXT:-Консольная сводка недоступна — см. файл.}"
  CAPTION="SLA-дашборд поставок FBS на СЦ — $TODAY$([ "$TEST_MODE" = 1 ] && echo ' [TEST]').
Файл-дашборд сообщением выше: скачать и открыть в браузере (работает офлайн).
Окна обновления: задания ${ORDERS_DAYS}д · статусы ${STATUS_DAYS}д · лента ${FEED_DAYS}д

${SUMMARY_TXT}"

  # 3a. Telegram — документ в чат
  log "── 3a/3: Telegram sendDocument → чат ${TG_CHAT_ID} ──"
  RUN_COUNT=$(( RUN_COUNT + 1 ))
  if tg_send_file "$REPORT_HTML" "$CAPTION"; then
    TG_OK=1
  else
    echo "⚠️  FAIL: Telegram-доставка не удалась" >&2
  fi

  # 3b. Почта — вложение + текстовое тело
  if [ "$MAIL_READY" = 1 ]; then
    log "── 3b/3: mail-notify (вложение $(du -h "$REPORT_HTML" | cut -f1)) ──"
    RUN_COUNT=$(( RUN_COUNT + 1 ))
    MAIL_BODY="SLA-дашборд поставок FBS на сортировочный центр.
Отчёт во вложении — открыть в браузере (интерактивный, работает офлайн).
Сгенерирован: $(date '+%Y-%m-%d %H:%M') · окна обновления: задания ${ORDERS_DAYS}д · статусы ${STATUS_DAYS}д · лента ${FEED_DAYS}д

${SUMMARY_TXT}"
    MAIL_ARGS=(--config "$MAIL_CFG" --subject "$SUBJECT" --attach "$REPORT_HTML")
    [ "$TEST_MODE" = 1 ] && MAIL_ARGS+=(--to "$TEST_TO")
    if printf '%s\n' "$MAIL_BODY" | go run "$PONCHO/cmd/ops-notify/mail-notify" "${MAIL_ARGS[@]}"; then
      MAIL_OK=1
    else
      echo "⚠️  FAIL: письмо не отправлено" >&2
    fi
  else
    log "── 3b/3: почта пропущена — SMTP_PASSWORD не задан в .env ──"
  fi
else
  log "── 3/3: пропуск — дашборд не собран ──"
fi

rm -f "$BUILD_OUT"

# ── Summary ──
ELAPSED=$(( SECONDS - START ))
log "═══════ Summary: ${RUN_COUNT} шагов за ${ELAPSED}s ═══════"
if [ "${#FAILED[@]}" -gt 0 ]; then
  log "⚠️  Упали фазы: ${FAILED[*]}"
  exit 1
fi
log "Доставка: Telegram=$([ "$TG_OK" = 1 ] && echo "ок" || echo "недоставлено") · почта=$([ "$MAIL_OK" = 1 ] && echo "отправлено" || echo "не отправлено")"
if [ -s "$REPORT_HTML" ] && [ "$TG_OK" = 0 ] && [ "$MAIL_OK" = 0 ]; then
  log "⚠️  Дашборд собран, но не доставлен ни по одному каналу"
  exit 1
fi
log "Все ок: данные обновлены, дашборд собран и доставлен."
