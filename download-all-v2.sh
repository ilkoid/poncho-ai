#!/bin/bash
# WB Full Data Refresh — VPS #2 edition: PG-only + cron-friendly
#
# Копия download-all.sh для VPS #2 «wb-tools» (ssh ilkoid@213.208.160.214 -p 16200),
# клон ожидается в ~/go-workspace/src/poncho-ai. Бэкенд — ТОЛЬКО PostgreSQL
# (SQLite-пасса нет), как принято на той машине.
#
# Отличия от download-all.sh:
#   + всё пишет в logs/download-all-v2-ГГГГ-ММ-ДД.log (таймстампы, ротация 30 дней)
#   + non-fatal git pull в начале (cron гоняет свежий код: go run, не bin/)
#   + PATH дополнен /usr/local/go/bin (в cron нет окружения юзера)
#   + общий LOCKDIR с download-all.sh — два полных прогона в одном чекауте
#     не должны писать PG параллельно
# Логика фаз, maint-группы, Summary — идентичны download-all.sh.
#
# Usage: bash download-all-v2.sh [days]   (days передаётся в утилиты с --days)
#        bash download-all-v2.sh --test-notify   (проверить доставку обоих каналов)
# Уведомления об ошибках: notify = Telegram primary → mail fallback.
# TG: тот же бот/чат, что у Mac-ночника (TG_BOT_TOKEN/TG_CHAT_ID из .env),
#     тег [VPS2 <hostname>]; при блокировке api.telegram.org — TG_PROXY.
# Mail: go run cmd/ops-notify/mail-notify (pkg/email, relay из его config.yaml),
#     креды — SMTP_PASSWORD в .env, кому — MAIL_TO из .env (override).
# Перед утилитой — целевая оптимизация её таблиц (maint <group>): VACUUM (ANALYZE)
# для upsert-churn, ANALYZE для снапшот-гигантов; группы — в pg-maintenance-PG.yaml.
# Фаза 7 — ANALYZE всех таблиц; по субботам Фаза 8 — REINDEX CONCURRENTLY.

# ── VPS #2: путь клона резолвится по месту скрипта; override — PONCHO_V2 ──
PONCHO="${PONCHO_V2:-$(cd "$(dirname "$0")" && pwd)}"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

# cron не читает профиль юзера: добавляем стандартные Linux-пути go
export PATH="$PATH:/usr/local/go/bin:${HOME}/go/bin"
command -v go >/dev/null 2>&1 || { echo "FAIL: go не найден в PATH (cron без профиля?) — установи go или допиши путь в PATH-строке скрипта" >&2; exit 1; }

# Примечание для crontab: символ % в строке запуска НЕ нужен и НЕ используется —
# все даты вычисляются внутри скрипта (там % безопасен).

# ── Logging: весь вывод скрипта (фазы, FAIL-метки, Summary) — в лог + stdout ──
LOGDIR="$PONCHO/logs"
mkdir -p "$LOGDIR"
LOGFILE="$LOGDIR/download-all-v2-$(date +%Y-%m-%d).log"
exec > >(tee -a "$LOGFILE") 2>&1

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# ── Telegram-уведомления об ошибках (креды TG_BOT_TOKEN/TG_CHAT_ID в .env,
#    тот же бот/чат, что у Mac-ночника; отличаем источник тегом машины —
#    TG_TAG задаётся после .env, так что переопределяется и из .env).
#    tg_send <text>: 0 = доставлено, 1 = нет (прогон не роняет). ──
tg_send() {
  local text="$1" resp
  if [ -z "${TG_BOT_TOKEN:-}" ] || [ -z "${TG_CHAT_ID:-}" ]; then
    log "WARNING: TG_BOT_TOKEN/TG_CHAT_ID не заданы в ${PONCHO}/.env — уведомление пропущено"
    return 1
  fi
  # TG_PROXY (напр. socks5h://127.0.0.1:1080) — если api.telegram.org недоступен
  # напрямую (RU-хостинг): только телеграм-запросы идут через прокси.
  # socks5h (не socks5!): DNS резолвится на стороне прокси — важно при DNS-блокировках.
  local proxy_args=()
  if [ -n "${TG_PROXY:-}" ]; then
    proxy_args=(--proxy "${TG_PROXY}")
  fi
  resp=$(curl -s --connect-timeout 10 --max-time 30 "${proxy_args[@]}" \
    "https://api.telegram.org/bot${TG_BOT_TOKEN}/sendMessage" \
    --data-urlencode "chat_id=${TG_CHAT_ID}" \
    --data-urlencode "text=${text}")
  if printf '%s' "$resp" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    return 0
  fi
  log "WARNING: Telegram отклонил отправку: ${resp:0:300}"
  return 1
}

# mail_send <text>: статус на почту через cmd/ops-notify/mail-notify (go run,
# отправщик pkg/email — работает с этим relay годами, в отличие от curl
# smtp://: по IP exit 60 — сертификат CN не совпадает с IP; по хостнейму
# exit 94 — AUTH-механизм Exchange). Креды: SMTP_PASSWORD в .env; MAIL_TO в .env
# переопределяет получателей config.yaml. Тело — в stdin, тема = первая строка.
mail_send() {
  local text="$1" subj rc
  if [ -z "${MAIL_TO:-}" ]; then log "WARNING: MAIL_TO не задан в ${PONCHO}/.env"; return 1; fi
  if [ -z "${SMTP_PASSWORD:-}" ]; then log "WARNING: SMTP_PASSWORD не задан в ${PONCHO}/.env"; return 1; fi
  subj="${TG_TAG}: ${text%%$'\n'*}"; subj=${subj:0:180}
  printf '%s' "$text" | go run "$PONCHO/cmd/ops-notify/mail-notify" \
    --config "$PONCHO/cmd/ops-notify/mail-notify/config.yaml" \
    --subject "$subj" --to "$MAIL_TO"
  rc=$?
  if [ "$rc" -eq 0 ]; then return 0; fi
  log "WARNING: SMTP-отправка не удалась (mail-notify exit ${rc})"; return 1
}

# notify <text>: TG primary → mail fallback. TG без кредов/прокси вернёт 1 —
# письмо уйдёт; на машине с рабочим TG почта не дублируется.
notify() {
  tg_send "$1" || mail_send "$1"
}

# ── Cleanup old logs (30 days) ──
find "$LOGDIR" -name '*.log' -mtime +30 -delete 2>/dev/null || true

# ── Load .env if present (секреты: ключи WB, PG_PWD, TG_BOT_TOKEN/TG_CHAT_ID) ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
  log "Loaded env from $PONCHO/.env"
else
  log "WARNING: $PONCHO/.env не найден — ключи WB берутся из окружения"
fi

# ── Тег машины в TG-сообщениях (после .env — можно переопределить оттуда) ──
TG_TAG="${TG_TAG:-[VPS2 $(hostname -s 2>/dev/null || hostname)]}"

# ── --test-notify: проверить оба канала раздельно и выйти ──
if [ "$DAYS" = "--test-notify" ]; then
  DAYS=""
  delivered=0
  log "--- тест Telegram ---"
  if tg_send "✅ ${TG_TAG} download-all-v2.sh: тест Telegram OK ($(date '+%Y-%m-%d %H:%M:%S'), host $(hostname))"; then
    log "OK: Telegram доставлен"
    delivered=1
  else
    log "Telegram недоступен/не настроен (не блокирует переход — fallback на mail)"
  fi
  log "--- тест mail ---"
  if mail_send "✅ ${TG_TAG} download-all-v2.sh: тест mail OK ($(date '+%Y-%m-%d %H:%M:%S'), host $(hostname))"; then
    log "OK: письмо доставлено на ${MAIL_TO}"
    delivered=1
  else
    log "FAIL: письмо не доставлено (подробности выше)"
  fi
  [ "$delivered" -eq 1 ] || { log "FAIL: ни один канал не доставил сообщение"; exit 1; }
  exit 0
fi

# ── PG defaults ПОСЛЕ .env (прод этой VPS; .env/export переопределяют) ──
# Фикс PGUSER→PG_ADMIN из локального download-all-pg.sh сохранён:
# приоритет PGUSER > PG_ADMIN > arm_ai_admin.
export PGHOST="${PGHOST:-10.120.24.155}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-${PG_ADMIN:-arm_ai_admin}}"

# ── git pull (non-fatal) ──
log "--- git pull ---"
if git -C "$PONCHO" pull 2>&1; then
  log "git pull OK"
else
  log "WARNING: git pull failed, continuing with existing code"
fi

# ── Single-instance lock (общий с download-all.sh) ──
# Протухший lock: mkdir-lock от убитого прогона (kill -9, OOM, ребут) сам не
# исчезнет — считаем брошенным и удаляем, если он старше 12 часов (-mmin +720 =
# «изменён больше 720 мин назад»; полярность веток обратна run-nightly-download.sh,
# там матч = свежий → SKIP). rmdir (не rm -r): пустой каталог удаляем молча,
# с содержимым — падаем громко. Легитимный прогон не потеряет lock: каждый шаг
# (run/maint) тачаит lockdir, так что окно 12ч отсчитывается от прогресса.
LOCKDIR="$PONCHO/.download-all.lock"
if [ -d "$LOCKDIR" ] && [ -n "$(find "$(dirname "$LOCKDIR")" -maxdepth 1 -name "$(basename "$LOCKDIR")" -type d -mmin +720 2>/dev/null)" ]; then
  log "WARNING: протухший lock (${LOCKDIR}, старше 12ч) — удаляю и продолжаю"
  rmdir "$LOCKDIR" 2>/dev/null || { log "FAIL: lock-каталог не пуст — разбери вручную: $LOCKDIR"; exit 1; }
fi
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  log "SKIP: другой прогон уже идёт (lock: $LOCKDIR)"
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable (протокол PG, не nc -z — см. кейс 05-06.09.2026) ──
PG_HOST="${PGHOST}"; PG_PORT="${PGPORT}"
PG_ISREADY="$(command -v pg_isready || echo /usr/lib/postgresql/bin/pg_isready)"
if [ -x "$PG_ISREADY" ]; then
  "$PG_ISREADY" -h "$PG_HOST" -p "$PG_PORT" -t 5 >/dev/null 2>&1 || PG_DOWN=1
else
  nc -z -w 5 "$PG_HOST" "$PG_PORT" 2>/dev/null || PG_DOWN=1
fi
if [ "${PG_DOWN:-0}" = "1" ]; then
  log "FAIL: PostgreSQL $PG_HOST:$PG_PORT не отвечает. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env"
  notify "⛔ ${TG_TAG} download-all-v2: PG недоступен — прогон прерван
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S')
PG: ${PG_HOST}:${PG_PORT}
лог: ${LOGFILE}" || true
  exit 1
fi

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
FAILED=()
RUN_COUNT=0

# run — обёртка над "go run <pkg> …". Имя утилиты = basename каталога из $3.
# touch lockdir: живой прогон обновляет mtime lock → окно протухшести 12ч
# отсчитывается от последнего шага, а не от старта прогона.
run() {
  local name; name="$(basename "$3")"
  RUN_COUNT=$((RUN_COUNT + 1))
  touch "${LOCKDIR:-/nonexistent}" 2>/dev/null || true
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    log "⚠️  FAIL: $name (exit $rc)"
  fi
  return "$rc"
}

# maint <group> — целевая оптимизация таблиц утилиты ДО её запуска.
# Падение НЕ блокирует загрузку — в Summary попадает как pg-maintenance[<group>].
maint() {
  RUN_COUNT=$((RUN_COUNT + 1))
  touch "${LOCKDIR:-/nonexistent}" 2>/dev/null || true
  go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --group "$1"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("pg-maintenance[$1] (exit $rc)")
    log "⚠️  FAIL: pg-maintenance[$1] (exit $rc)"
  fi
  return "$rc"
}

START=$SECONDS
log "═══════  WB Full Refresh v2 (PostgreSQL) started  ═══════"

###############################################################################
#  Phase 1: Catalog
###############################################################################

log "── Phase 1: Catalog ──"

maint cards
run go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres
maint prices
run go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices-PG.yaml" --backend postgres
maint onec-data
maint onec-prices
run go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2-PG.yaml" --backend postgres
maint onec-rests
run go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-PG.yaml" --backend postgres

###############################################################################
#  Phase 2: Feedbacks
###############################################################################

log "── Phase 2: Feedbacks ──"

maint feedbacks
run go run "$PONCHO/cmd/data-downloaders/download-wb-feedbacks-v2" --config "$C/download-wb-feedbacks-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

###############################################################################
#  Phase 3: Sales & Revenue
###############################################################################

log "── Phase 3: Sales & Revenue ──"

maint orders
run go run "$PONCHO/cmd/data-downloaders/download-wb-orders-v2" --config "$C/download-wb-orders-PG.yaml" --backend postgres
maint opsales
run go run "$PONCHO/cmd/data-downloaders/download-wb-opsales-v2" --config "$C/download-wb-opsales-PG.yaml" --backend postgres
maint sales
run go run "$PONCHO/cmd/data-downloaders/download-wb-sales-v2" --config "$C/download-wb-sales-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint region-sales
run go run "$PONCHO/cmd/data-downloaders/download-wb-region-sales-v2" --config "$C/download-wb-region-sales-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

###############################################################################
#  Phase 4: Stock & Logistics
###############################################################################

log "── Phase 4: Stock & Logistics ──"

maint stocks
run go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
maint stock-products
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
maint stock-history
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint stock-history-metrics
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-history-v2" --config "$C/download-wb-stock-history-metrics-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint supplies
run go run "$PONCHO/cmd/data-downloaders/download-wb-supplies-v2" --config "$C/download-wb-supplies-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint fbs-orders
run go run "$PONCHO/cmd/data-downloaders/download-wb-fbs-orders-v2" --config "$C/download-wb-fbs-orders-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

###############################################################################
#  Phase 5: Advertising
###############################################################################

log "── Phase 5: Advertising ──"

#maint campaigns
run go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres
#maint promotion
#run go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

###############################################################################
#  Phase 6: Analytics
###############################################################################

log "── Phase 6: Analytics ──"

#run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-v2" --config "$C/download-wb-funnel-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint funnel-agg
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
# Самодолив дырок funnel-агрегатов: ночное окно, убитое 429-штормом, не
# самолечется — здесь находим отсутствующие/частичные окна за 2 недели и
# перекачиваем явными датами. Нефатально для прогона.
bash "$PONCHO/refill-funnel-holes.sh" || log "⚠️  refill-funnel-holes: сбой (нефатально)"
maint funnel-csv
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint search-vis
run go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint penalties
run go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2-PG.yaml" --backend postgres
maint whremains
run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)

###############################################################################
#  Phase 7: PG Maintenance — финальный лёгкий проход (ANALYZE всех таблиц)
###############################################################################

log "── Phase 7: PG Maintenance (ANALYZE all) ──"

run go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --analyze-only

###############################################################################
#  Phase 8: Weekly deep pass — REINDEX CONCURRENTLY (без локов, ~10-20 мин).
#  Только по субботам; прерванная суббота догонится через неделю.
###############################################################################

if [ "$(date +%u)" = "6" ]; then
  log "── Phase 8: Weekly REINDEX CONCURRENTLY ──"
  run go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --reindex-concurrently
else
  log "── Phase 8: skipped (REINDEX только по субботам; сегодня $(date +%A)) ──"
fi

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
log "═══════  Summary  ═══════"
if [ "${#FAILED[@]}" -eq 0 ]; then
  log "✔ Все утилиты завершились успешно ($RUN_COUNT/$RUN_COUNT)"
  # Heartbeat: тишина двусмысленна (нет письма = и «всё ок», и «мёртвый cron») —
  # успешный прогон тоже сообщает о себе.
  notify "✔️ ${TG_TAG} download-all-v2: все шаги OK ($RUN_COUNT/$RUN_COUNT), $((TOTAL / 60))m
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S')
Лог: ${LOGFILE}" || true
else
  log "✗ Не выполнились (${#FAILED[@]} из $RUN_COUNT):"
  printf '  ✗ %s\n' "${FAILED[@]}"
  # ── Уведомление: TG primary → mail fallback, одно на прогон ──
  fail_lines=$(printf '  ✗ %s\n' "${FAILED[@]}")
  tail_log=$(tail -n 15 "$LOGFILE" 2>/dev/null)
  notify "⛔ ${TG_TAG} download-all-v2: упали ${#FAILED[@]} из ${RUN_COUNT} шагов
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S'), $((TOTAL / 60))m

Упавшие утилиты:
${fail_lines}

Лог: ${LOGFILE}

Хвост лога:
${tail_log}" || true
fi
log "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"

[ "${#FAILED[@]}" -eq 0 ] || exit 1
