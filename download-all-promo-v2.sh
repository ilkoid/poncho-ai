#!/bin/bash
# WB Промо-блок VPS #2 — штормовые ручки аг-рекламы/аг-аналитики, вынесенные из
# ночного download-all-v2.sh в дневной слот (крон 10:00).
#
# Состав: campaigns, promotion, funnel-agg, funnel-csv, search-vis + финальный
# ANALYZE. Всё остальное (контент/1С/продажи/остатки/penalties/item-rating/
# whremains) остаётся в ночном download-all-v2.sh (крон 00:05).
#
# Почему днём: аг-адверт днём спокойнее (замер 15.10: 14×429 в полдень против
# 1816×429 ночью 17.10), окно 10:00–16:00 свободно от пересечений с Mac (его
# промо-блок 05:30 финишем ~08:50). Зависимость: promotion читает campaign_products
# свежего campaigns — порядок фаз не менять. Днём вселенная пар «жирная»
# (авто-кампании активны, ~110 тыс.) — блок идёт ~5–6ч, укладывается до вечера.
#
# Отличия от download-all-v2.sh (инфраструктура скопирована):
#   + лог logs/download-all-promo-v2-ГГГГ-ММ-ДД.log, TG_TAG с суффиксом «promo»
#   + СВОЙ lock .download-all-promo.lock (не пересекаться с ночным .download-all.lock)
#     с окном протухшести 4ч (а не 12ч): прогон стартует 10:00, утечка ночного
#     лока не должна его скипать; свои же шаги тачат lock
#   + НЕ общий LOCKDIR с ночным прогоном: ночи и день последовательны, общий не нужен
#
# Usage: bash download-all-promo-v2.sh [days]
#        bash download-all-promo-v2.sh --test-notify   (проверить доставку обоих каналов)
# Уведомления: Telegram primary → mail fallback; heartbeat при успехе.

# ── VPS #2: путь клона резолвится по месту скрипта; override — PONCHO_V2 ──
PONCHO="${PONCHO_V2:-$(cd "$(dirname "$0")" && pwd)}"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

# cron не читает профиль юзера: добавляем стандартные Linux-пути go
export PATH="$PATH:/usr/local/go/bin:${HOME}/go/bin"
command -v go >/dev/null 2>&1 || { echo "FAIL: go не найден в PATH (cron без профиля?) — установи go или допиши путь в PATH-строке скрипта" >&2; exit 1; }

# go run <пакет> требует cwd внутри модуля
cd "$PONCHO" || { echo "FAIL: не могу зайти в $PONCHO" >&2; exit 1; }

# Примечание для crontab: символ % в строке запуска НЕ нужен и НЕ используется —
# все даты вычисляются внутри скрипта (там % безопасен).

# ── Logging: весь вывод скрипта — в лог + stdout ──
LOGDIR="$PONCHO/logs"
mkdir -p "$LOGDIR"
LOGFILE="$LOGDIR/download-all-promo-v2-$(date +%Y-%m-%d).log"
exec > >(tee -a "$LOGFILE") 2>&1

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# ── Telegram-уведомления (креды TG_BOT_TOKEN/TG_CHAT_ID в .env). ──
#    tg_send <text>: 0 = доставлено, 1 = нет (прогон не роняет). ──
tg_send() {
  local text="$1" resp
  if [ -z "${TG_BOT_TOKEN:-}" ] || [ -z "${TG_CHAT_ID:-}" ]; then
    log "WARNING: TG_BOT_TOKEN/TG_CHAT_ID не заданы в ${PONCHO}/.env — уведомление пропущено"
    return 1
  fi
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

# mail_send <text>: статус на почту через cmd/ops-notify/mail-notify.
# Креды: SMTP_PASSWORD в .env; MAIL_TO в .env переопределяет получателей.
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

# notify <text>: TG primary → mail fallback.
notify() {
  tg_send "$1" || mail_send "$1"
}

# ── Cleanup old logs (30 days) ──
find "$LOGDIR" -name '*.log' -mtime +30 -delete 2>/dev/null || true

# ── Load .env if present ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
  log "Loaded env from $PONCHO/.env"
else
  log "WARNING: $PONCHO/.env не найден — ключи WB берутся из окружения"
fi

# ── Тег машины в TG-сообщениях (после .env): суффикс promo отличает от ночника ──
TG_TAG="${TG_TAG:-[VPS2 $(hostname -s 2>/dev/null || hostname)] promo}"

# ── --test-notify: проверить оба канала раздельно и выйти ──
if [ "$DAYS" = "--test-notify" ]; then
  DAYS=""
  delivered=0
  log "--- тест Telegram ---"
  if tg_send "✅ ${TG_TAG} download-all-promo-v2.sh: тест Telegram OK ($(date '+%Y-%m-%d %H:%M:%S'), host $(hostname))"; then
    log "OK: Telegram доставлен"
    delivered=1
  else
    log "Telegram недоступен/не настроен (не блокирует переход — fallback на mail)"
  fi
  log "--- тест mail ---"
  if mail_send "✅ ${TG_TAG} download-all-promo-v2.sh: тест mail OK ($(date '+%Y-%m-%d %H:%M:%S'), host $(hostname))"; then
    log "OK: письмо доставлено на ${MAIL_TO}"
    delivered=1
  else
    log "FAIL: письмо не доставлено (подробности выше)"
  fi
  [ "$delivered" -eq 1 ] || { log "FAIL: ни один канал не доставил сообщение"; exit 1; }
  exit 0
fi

# ── PG defaults ПОСЛЕ .env (прод этой VPS; .env/export переопределяют) ──
export PGHOST="${PGHOST:-10.120.24.155}"
export PGPORT="${PGPORT:-5432}"
export PGUSER="${PGUSER:-${PG_ADMIN:-arm_ai_admin}}"

# ── git pull (non-fatal): дневной прогон гоняет свежий код/конфиги ──
log "--- git pull ---"
if git -C "$PONCHO" pull 2>&1; then
  log "git pull OK"
else
  log "WARNING: git pull failed, continuing with existing code"
fi

# ── Single-instance lock: СВОЙ каталог (день), окно протухшести 4ч.
# Протухший lock от убитого прогона (kill -9, OOM) сам не исчезнет: 4ч без
# прогресса (шаги тачат lockdir) = брошен. НЕ 12ч как у ночника: старт 10:00,
# ночная утечка (последний touch ≤02:00) = 8ч — должна считаться протухшей,
# иначе молча скипнем дневной прогон.
LOCKDIR="$PONCHO/.download-all-promo.lock"
if [ -d "$LOCKDIR" ] && [ -n "$(find "$(dirname "$LOCKDIR")" -maxdepth 1 -name "$(basename "$LOCKDIR")" -type d -mmin +240 2>/dev/null)" ]; then
  log "WARNING: протухший lock (${LOCKDIR}, старше 4ч) — удаляю и продолжаю"
  rmdir "$LOCKDIR" 2>/dev/null || { log "FAIL: lock-каталог не пуст — разбери вручную: $LOCKDIR"; exit 1; }
fi
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  log "SKIP: другой промо-прогон уже идёт (lock: $LOCKDIR)"
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable ──
PG_HOST="${PGHOST}"; PG_PORT="${PGPORT}"
PG_ISREADY="$(command -v pg_isready || echo /usr/lib/postgresql/bin/pg_isready)"
if [ -x "$PG_ISREADY" ]; then
  "$PG_ISREADY" -h "$PG_HOST" -p "$PG_PORT" -t 5 >/dev/null 2>&1 || PG_DOWN=1
else
  nc -z -w 5 "$PG_HOST" "$PG_PORT" 2>/dev/null || PG_DOWN=1
fi
if [ "${PG_DOWN:-0}" = "1" ]; then
  log "FAIL: PostgreSQL $PG_HOST:$PG_PORT не отвечает. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env"
  notify "⛔ ${TG_TAG} download-all-promo-v2: PG недоступен — прогон прерван
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S')
PG: ${PG_HOST}:${PG_PORT}
лог: ${LOGFILE}" || true
  exit 1
fi

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
FAILED=()
RUN_COUNT=0

# run — обёртка над "go run <pkg> …". Имя утилиты = basename каталога из $3.
# touch lockdir: живой прогон обновляет mtime lock → окно протухшести 4ч
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
log "═══════  WB Promo Block v2 (PostgreSQL) started  ═══════"

# ── Phase 1: Campaigns — номенклатура кампаний = вселенная пар для promotion ──
log "── Phase 1: Campaigns ──"
maint campaigns
run go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres

# ── Phase 2: Promotion (normquery stats/list/bids/minus, min-bids, бюджеты) —
#    аг-адверт; ретраи «подсказка+ε» + rescue уже в конфиге ──
log "── Phase 2: Promotion ──"
maint promotion
run go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 3: Funnel (агрегатная воронка + недельный CSV-отчёт); днём сквозь
#    шторм аг-контентаналитикс — схема «подсказка+ε» + budget держат ──
log "── Phase 3: Funnel ──"
maint funnel-agg
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint funnel-csv
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 4: Search visibility ──
log "── Phase 4: Search visibility ──"
maint search-vis
run go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 5: PG Maintenance — ANALYZE всех таблиц ──
log "── Phase 5: PG Maintenance (ANALYZE all) ──"
run go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --analyze-only

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
log "═══════  Summary  ═══════"
if [ "${#FAILED[@]}" -eq 0 ]; then
  log "✔ Все утилиты завершились успешно ($RUN_COUNT/$RUN_COUNT)"
  # Heartbeat: успешный прогон тоже сообщает о себе.
  notify "✔️ ${TG_TAG} download-all-promo-v2: все шаги OK ($RUN_COUNT/$RUN_COUNT), $((TOTAL / 60))m
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S')
Лог: ${LOGFILE}" || true
else
  log "✗ Не выполнились (${#FAILED[@]} из $RUN_COUNT):"
  printf '  ✗ %s\n' "${FAILED[@]}"
  fail_lines=$(printf '  ✗ %s\n' "${FAILED[@]}")
  tail_log=$(tail -n 15 "$LOGFILE" 2>/dev/null)
  notify "⛔ ${TG_TAG} download-all-promo-v2: упали ${#FAILED[@]} из ${RUN_COUNT} шагов
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S'), $((TOTAL / 60))m

Упавшие утилиты:
${fail_lines}

Лог: ${LOGFILE}

Хвост лога:
${tail_log}" || true
fi
log "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"

[ "${#FAILED[@]}" -eq 0 ] || exit 1
