#!/bin/bash
# run-nightly-download.sh — ночной прогон download-all.sh по расписанию (launchd, 02:30).
#
# Что добавляет к download-all.sh:
#   • логи: logs/download-all-ГГГГ-ММ-ДД.log (ротация 30 дней) + строка статуса в logs/nightly-status.log
#   • удаление ПРОТУХШЕГО lock (.download-all.lock старше 12ч — сбой питания, trap не успел):
#     без этого все ночи после сбоя молча уходят в SKIP
#   • caffeinate -is — сон Mac не рвёт длинные фазы (см. шапку download-all.sh)
#   • Telegram-уведомление при падении прогонов (TG_BOT_TOKEN + TG_CHAT_ID в .env)
#
# Usage:
#   bash run-nightly-download.sh               # ночной прогон (то же, что делает launchd)
#   bash run-nightly-download.sh --test-notify # проверить доставку Telegram
#   bash run-nightly-download.sh --selftest    # самотест логики lock/логов, без загрузки данных
#   sudo bash run-nightly-download.sh --install-daemon   # LaunchDaemon от root (рекомендую)
#   sudo bash run-nightly-download.sh --uninstall-daemon
#   bash run-nightly-download.sh --install     # LaunchAgent (запасной вариант, см. ниже)
#   bash run-nightly-download.sh --uninstall   # удалить LaunchAgent
#
# LaunchDaemon: /Library/LaunchDaemons/com.ilkoid.poncho.download-all.plist — запуск от root.
# ЭТО ОСНОВНОЙ РЕЖИМ: macOS Local Network Privacy блокирует LAN-доступ не-Apple бинарникам
# в контексте LaunchAgent (кейс 05-06.09.2026: nc из-под launchd подключался, а Go/pg_isready
# получали no route to host при живом сервере; из Терминала всё работало). Root-демоны TCC
# не гейтит; бонус — стартует при загрузке до логина, автовход не нужен. Домашние каталоги
# Go подменяются через EnvironmentVariables (HOME), чтобы не качать модули заново.
#
# LaunchAgent: ~/Library/LaunchAgents/com.ilkoid.poncho.download-all.plist — работает только
# если LAN-доступ не блокируется (после выдачи разрешений в Privacy & Security → Local Network);
# запуск в залогиненной сессии, после ребута нужен автовход.
#
# launchd, в отличие от cron, не теряет пропущенный запуск: если Mac спал в 02:30 —
# job стартует при пробуждении.
#
# Чек-лист перед отпуском:
#   1. Закоммитить рабочий каталог — расписание гоняет код как есть (download-all.sh = go run).
#   2. Заполнить TG_BOT_TOKEN/TG_CHAT_ID в .env → проверить: --test-notify.
#   3. --install → один контрольный ручной прогон без аргументов.
#   4. Mac: автовход включён (LaunchAgent живёт в залогиненной сессии после ребута);
#      сон на время отпуска выключить: sudo pmset -a sleep 0 disksleep 0 (потом вернуть).

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C_LOCK="$PONCHO/.download-all.lock"
LOGDIR="$PONCHO/logs"
ENV_FILE="$PONCHO/.env"
PLIST="$HOME/Library/LaunchAgents/com.ilkoid.poncho.download-all.plist"

# ожидание чужого прогона: лимит и период опроса (переопределяются для тестов)
WAIT_LIMIT_SECS="${WAIT_LIMIT_SECS:-14400}"
LOCK_POLL_SECS="${LOCK_POLL_SECS:-300}"

# владелец репо — для HOME/PATH в демоне и chown логов при запуске от root
GUI_USER="$(stat -f %Su "$PONCHO" 2>/dev/null || echo "$USER")"
GUI_HOME="$(dscl . -read "/Users/$GUI_USER" NFSHomeDirectory 2>/dev/null | awk '{print $2}')"
[ -n "$GUI_HOME" ] || GUI_HOME="/Users/$GUI_USER"
DAEMONS_DIR="${DAEMONS_DIR:-/Library/LaunchDaemons}"
DAEMON_PLIST="$DAEMONS_DIR/com.ilkoid.poncho.download-all.plist"
PROBE_DAEMON_PLIST="$DAEMONS_DIR/com.ilkoid.poncho.pg-probe.plist"

# ── PATH для launchd: там минимальное окружение; go и pg_isready живут в homebrew ──
BASE_PATH="/opt/homebrew/bin:/opt/homebrew/opt/libpq/bin:/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export PATH="$BASE_PATH:$PATH"

mkdir -p "$LOGDIR"
LOGFILE="${LOGDIR}/download-all-$(date +%Y-%m-%d).log"
STATUSFILE="${LOGDIR}/nightly-status.log"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOGFILE" >&2; }

# ── .env — канонический источник секретов (в VPS-стиле экспорт уже в окружении) ──
if [ -f "$ENV_FILE" ]; then
  set -a; . "$ENV_FILE"; set +a
else
  log "ERROR: $ENV_FILE не найден"
  exit 1
fi

# ensure_lock_free <lockdir>: 0 = можно идти (lock нет/протухший удалён), 2 = свежий lock
ensure_lock_free() {
  local lockdir="$1"
  [ -d "$lockdir" ] || return 0
  if [ -n "$(find "$(dirname "$lockdir")" -maxdepth 1 -name "$(basename "$lockdir")" -type d -mmin -720 2>/dev/null)" ]; then
    log "SKIP: свежий lock (<12ч) — другой прогон идёт: $lockdir"
    return 2
  fi
  log "WARNING: протухший lock (старше 12ч — сбой питания без trap?) → удаляю: $lockdir"
  rm -rf "$lockdir"
  return 0
}

# tg_send <text>: 0 = доставлено, 1 = нет (прогон не роняет)
tg_send() {
  local text="$1" resp
  if [ -z "${TG_BOT_TOKEN:-}" ] || [ -z "${TG_CHAT_ID:-}" ]; then
    log "WARNING: TG_BOT_TOKEN/TG_CHAT_ID не заданы в ${ENV_FILE} — уведомление пропущено"
    return 1
  fi
  resp=$(curl -s --connect-timeout 10 --max-time 30 \
    "https://api.telegram.org/bot${TG_BOT_TOKEN}/sendMessage" \
    --data-urlencode "chat_id=${TG_CHAT_ID}" \
    --data-urlencode "text=${text}")
  if printf '%s' "$resp" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    return 0
  fi
  log "WARNING: Telegram отклонил отправку: ${resp:0:300}"
  return 1
}

# pg_ok: честная проверка «PG реально отвечает», а не «TCP-порт открыт».
# Кейс 2026-09-05/06: ночью nc -z «подключался» к 192.168.10.7:15432 (за спящим
# хостом, видимо, SYN-прокси роутера), а реальные подключения получали
# no route to host. pg_isready ждёт ответа самого PG — его не обмануть.
pg_ok() {
  local host="${PGHOST:-192.168.10.7}" port="${PGPORT:-15432}"
  if [ -x /opt/homebrew/opt/libpq/bin/pg_isready ]; then
    /opt/homebrew/opt/libpq/bin/pg_isready -h "$host" -p "$port" -t 5 >/dev/null 2>&1
  else
    nc -z -w 5 "$host" "$port" 2>/dev/null
  fi
}

# wait_for_pg: ждём живого PG до 15 мин (30 × 30с); 0 = доступен, 1 = сдались.
wait_for_pg() {
  local host="${PGHOST:-192.168.10.7}" port="${PGPORT:-15432}" tries=0
  until pg_ok; do
    tries=$(( tries + 1 ))
    if [ "$tries" -ge 30 ]; then
      log "FAIL: PG $host:$port не отвечает дольше ~15 мин — сдаюсь"
      return 1
    fi
    log "WARNING: PG $host:$port не отвечает — жду 30с (попытка $tries/30)"
    sleep 30
  done
  return 0
}

# run_attempt: сам прогон download-all.sh под caffeinate (сон Mac не рвёт фазы)
run_attempt() {
  if [ -t 1 ]; then
    caffeinate -is bash "$PONCHO/download-all.sh" 2>&1 | tee -a "$LOGFILE"
    return "${PIPESTATUS[0]}"
  fi
  caffeinate -is bash "$PONCHO/download-all.sh" >> "$LOGFILE" 2>&1
  return $?
}

# wait_lock_free: если идёт чужой прогон (свежий lock) — ждём его освобождения,
# опрашивая каждые LOCK_POLL_SEС, но не дольше WAIT_LIMIT_SECS.
# 0 = можно идти, 1 = чужой прогон так и не освободил lock.
wait_lock_free() {
  local waited=0
  until ensure_lock_free "$C_LOCK"; do
    if [ "$waited" -ge "$WAIT_LIMIT_SECS" ]; then return 1; fi
    sleep "$LOCK_POLL_SECS"; waited=$(( waited + LOCK_POLL_SECS ))
  done
  return 0
}

nightly_run() {
  # ротация 30 дней — только логи этой обёртки
  find "$LOGDIR" -name 'download-all-*.log' -mtime +30 -delete 2>/dev/null

  # чужой прогон (например, вечерний ручной, застрявший за полночь) — не повод
  # пропускать ночь: ждём освобождения до 4ч, потом фиксируем SKIP в статусе
  if ! wait_lock_free; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] SKIP: чужой прогон держал lock дольше $(( WAIT_LIMIT_SECS / 3600 ))ч" >> "$STATUSFILE"
    log "SKIP: ожидание чужого прогона прекращено по лимиту ${WAIT_LIMIT_SECS}с"
    exit 0
  fi

  # граница «до прогона»: ошибки ищем только в своём участке лога (лог дневной, appends)
  LINES_BEFORE=0
  [ -f "$LOGFILE" ] && LINES_BEFORE=$(wc -l < "$LOGFILE")

  log "=== nightly download-all: старт ==="
  local rc elapsed fail_lines=""
  NIGHT_START=$SECONDS
  if ! wait_for_pg; then
    rc=1
    fail_lines="PG ${PGHOST:-192.168.10.7}:${PGPORT:-15432} не отвечает дольше ~15 мин (wait_for_pg)"
    log "FAIL: $fail_lines"
  else
    # Лестница ретраев: быстрый провал (<5 мин) — это инфраструктура (сеть/PG),
    # не данные; гоняем повторно с ростом паузы: ~02:30 → 02:45 → 03:15 → 04:15.
    # Долгий прогон, упавший в конце, — данные/API, ретраи бессмысленны (break).
    local delay
    for delay in 0 900 1800 3600; do
      if [ "$delay" -gt 0 ]; then
        log "WARNING: быстрый провал — сеть/PG? Следующая попытка через $(( delay / 60 )) мин."
        sleep "$delay"
        wait_for_pg || log "WARNING: PG всё ещё не отвечает — пробуем всё равно"
        log "=== nightly download-all: ретрай ==="
      fi
      RUN_START=$SECONDS
      run_attempt; rc=$?
      elapsed=$(( SECONDS - RUN_START ))
      if [ "$rc" -eq 0 ] || [ "$elapsed" -ge 300 ]; then break; fi
    done
  fi
  # в статус уходит общее время ночи (с ожиданиями), в минутах
  elapsed=$(( (SECONDS - NIGHT_START) / 60 ))

  local section
  section=$(tail -n "+$(( LINES_BEFORE + 1 ))" "$LOGFILE")
  [ -n "$fail_lines" ] || fail_lines=$(printf '%s\n' "$section" | grep 'FAIL' | head -20)

  local status="OK"; [ "$rc" -ne 0 ] && status="FAIL(rc=$rc)"
  {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ${status} ${elapsed}m host=$(hostname)"
    [ -n "$fail_lines" ] && printf '%s\n' "$fail_lines" | sed 's/^/    /'
  } >> "$STATUSFILE"

  if [ "$rc" -ne 0 ]; then
    local text tail_log
    tail_log=$(tail -30 "$LOGFILE")
    text="⛔ poncho: ночной download-all упал
host: $(hostname), $(date '+%Y-%m-%d %H:%M:%S'), exit=${rc}, ${elapsed}m
лог: ${LOGFILE}

Упавшие утилиты:
${fail_lines:-—}

Хвост лога:
${tail_log}"
    text=${text:0:3500}
    tg_send "$text"
  fi
  log "=== nightly download-all: финиш ${status}, ${elapsed}m ==="
  # при запуске от root (LaunchDaemon) логи и кэш Go остаются подвластны пользователю:
  # daemon идёт с HOME пользователя (тёплый кэш компиляций), но созданные root файлы
  # в go-build иначе сломали бы ручные go run из-под пользователя
  if [ "$(id -u)" -eq 0 ]; then
    chown -R "${GUI_USER}:staff" "$LOGDIR" 2>/dev/null
    chown -R "${GUI_USER}:staff" "${HOME:-/Users/$GUI_USER}/Library/Caches/go-build" 2>/dev/null
  fi
  exit "$rc"
}

cmd_test_notify() {
  tg_send "✅ poncho run-nightly-download.sh: тест уведомлений OK ($(date '+%Y-%m-%d %H:%M:%S'), host $(hostname))" \
    && echo "OK: сообщение доставлено в Telegram" \
    || { echo "FAIL: сообщение не доставлено (подробности выше)"; exit 1; }
}

cmd_install() {
  mkdir -p "$(dirname "$PLIST")" "$LOGDIR"
  cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.ilkoid.poncho.download-all</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>${PONCHO}/run-nightly-download.sh</string>
    </array>
    <key>WorkingDirectory</key>
    <string>${PONCHO}</string>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>2</integer>
        <key>Minute</key>
        <integer>30</integer>
    </dict>
    <key>StandardOutPath</key>
    <string>${LOGDIR}/launchd.log</string>
    <key>StandardErrorPath</key>
    <string>${LOGDIR}/launchd.log</string>
</dict>
</plist>
EOF
  plutil -lint "$PLIST" || { echo "FAIL: plist невалиден"; exit 1; }
  launchctl unload "$PLIST" >/dev/null 2>&1
  if launchctl load -w "$PLIST"; then
    echo "OK: LaunchAgent загружен — ежедневно в 02:30"
    echo "     $PLIST"
    echo "Проверка: launchctl list | grep poncho"
  else
    echo "FAIL: launchctl load" >&2
    exit 1
  fi
}

cmd_uninstall() {
  launchctl unload -w "$PLIST" >/dev/null 2>&1
  rm -f "$PLIST"
  echo "OK: LaunchAgent удалён ($PLIST)"
}

# LaunchDaemon от root: обходит Local Network Privacy (блокирует LAN не-Apple
# бинарникам в LaunchAgent-контексте) и стартует при загрузке до логина.
# Заодно ставит диагностический probe-демон (pg-probe.sh каждые 10 мин).
cmd_install_daemon() {
  local real=0; [ "$DAEMONS_DIR" = "/Library/LaunchDaemons" ] && real=1
  if [ "$real" -eq 1 ] && [ "$(id -u)" -ne 0 ]; then
    echo "FAIL: --install-daemon запускается с sudo" >&2
    exit 1
  fi
  mkdir -p "$DAEMONS_DIR" "$LOGDIR"

  cat > "$DAEMON_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>com.ilkoid.poncho.download-all</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>${PONCHO}/run-nightly-download.sh</string>
    </array>
    <key>WorkingDirectory</key>
    <string>${PONCHO}</string>
    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key><integer>2</integer>
        <key>Minute</key><integer>30</integer>
    </dict>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key><string>${GUI_HOME}</string>
        <key>USER</key><string>${GUI_USER}</string>
        <key>LOGNAME</key><string>${GUI_USER}</string>
        <key>PATH</key><string>${BASE_PATH}</string>
    </dict>
    <key>StandardOutPath</key><string>${LOGDIR}/launchd-daemon.log</string>
    <key>StandardErrorPath</key><string>${LOGDIR}/launchd-daemon.log</string>
</dict>
</plist>
EOF

  cat > "$PROBE_DAEMON_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>com.ilkoid.poncho.pg-probe</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>${PONCHO}/pg-probe.sh</string>
    </array>
    <key>StartInterval</key><integer>600</integer>
    <key>RunAtLoad</key><true/>
</dict>
</plist>
EOF

  if [ "$real" -eq 0 ]; then
    echo "TEST-режим (DAEMONS_DIR=$DAEMONS_DIR): plists записаны, load пропущен"
    plutil -lint "$DAEMON_PLIST" && plutil -lint "$PROBE_DAEMON_PLIST"
    return
  fi

  chown root:wheel "$DAEMON_PLIST" "$PROBE_DAEMON_PLIST"
  chmod 644 "$DAEMON_PLIST" "$PROBE_DAEMON_PLIST"
  plutil -lint "$DAEMON_PLIST" && plutil -lint "$PROBE_DAEMON_PLIST" || { echo "FAIL: plist невалиден" >&2; exit 1; }

  # пользовательские агенты прочь — иначе дубль в 02:30
  local uid; uid="$(stat -f %u "$PONCHO")"
  launchctl bootout "gui/$uid/com.ilkoid.poncho.download-all" 2>/dev/null
  launchctl bootout "gui/$uid/com.ilkoid.poncho.pg-probe" 2>/dev/null
  rm -f "${GUI_HOME}/Library/LaunchAgents/com.ilkoid.poncho.download-all.plist" \
        "${GUI_HOME}/Library/LaunchAgents/com.ilkoid.poncho.pg-probe.plist"

  launchctl load -w "$PROBE_DAEMON_PLIST" || { echo "FAIL: probe daemon load" >&2; exit 1; }
  if launchctl load -w "$DAEMON_PLIST"; then
    chown -R "${GUI_USER}:staff" "$LOGDIR" 2>/dev/null
    echo "OK: LaunchDaemon загружен — ежедневно 02:30, от root (LAN-приватность обходится)"
    echo "     $DAEMON_PLIST"
    echo "     probe-демон: каждые 10 мин → logs/pg-probe.log"
    echo "Проверка: sudo launchctl list | grep poncho"
  else
    echo "FAIL: launchctl load" >&2
    exit 1
  fi
}

cmd_uninstall_daemon() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "FAIL: --uninstall-daemon запускается с sudo" >&2
    exit 1
  fi
  launchctl bootout "system/com.ilkoid.poncho.download-all" 2>/dev/null
  launchctl bootout "system/com.ilkoid.poncho.pg-probe" 2>/dev/null
  launchctl unload -w "$DAEMON_PLIST" 2>/dev/null
  launchctl unload -w "$PROBE_DAEMON_PLIST" 2>/dev/null
  rm -f "$DAEMON_PLIST" "$PROBE_DAEMON_PLIST"
  echo "OK: LaunchDaemons удалены (LaunchAgent можно вернуть: bash $0 --install)"
}

cmd_selftest() {
  local T fails=0
  T=$(mktemp -d)
  LOGFILE="$T/selftest.log"   # не мусорим в дневной лог
  # 1) lock отсутствует → ok
  ensure_lock_free "$T/absent.lock"; [ $? -eq 0 ] || { echo "FAIL: нет lock → должен быть 0"; fails=1; }
  # 2) протухший lock (2020 год) → удалён, ok
  mkdir "$T/stale.lock" && touch -t 202001010000 "$T/stale.lock"
  ensure_lock_free "$T/stale.lock"; [ $? -eq 0 ] || { echo "FAIL: протухший lock → должен быть 0"; fails=1; }
  [ -d "$T/stale.lock" ] && { echo "FAIL: протухший lock не удалён"; fails=1; }
  # 3) свежий lock → 2 (SKIP), не тронут
  mkdir "$T/fresh.lock"
  ensure_lock_free "$T/fresh.lock"; [ $? -eq 2 ] || { echo "FAIL: свежий lock → должен быть 2"; fails=1; }
  [ -d "$T/fresh.lock" ] || { echo "FAIL: свежий lock удалён (не должен был)"; fails=1; }
  # 4) tg_send без кред → 1, без падения
  ( export TG_BOT_TOKEN="" TG_CHAT_ID=""
    tg_send "не должно уйти" ) ; [ $? -eq 1 ] || { echo "FAIL: tg_send без кред → должен быть 1"; fails=1; }
  # 5) wait_lock_free: lock свободен → 0 сразу
  C_LOCK="$T/absent.lock" WAIT_LIMIT_SECS=2 LOCK_POLL_SECS=1
  wait_lock_free; [ $? -eq 0 ] || { echo "FAIL: wait_lock_free без lock → должен быть 0"; fails=1; }
  # 6) wait_lock_free: свежий lock не освобождается → 1 по лимиту (~2с)
  C_LOCK="$T/fresh2.lock"; mkdir "$T/fresh2.lock"
  wait_lock_free; [ $? -eq 1 ] || { echo "FAIL: wait_lock_free с вечным lock → должен быть 1"; fails=1; }
  rm -rf "$T"
  if [ "$fails" -eq 0 ]; then echo "SELFTEST PASS (lock-логика, tg-деградация)"; else echo "SELFTEST FAIL"; exit 1; fi
}

case "${1:-}" in
  --test-notify)     cmd_test_notify ;;
  --install)         cmd_install ;;
  --uninstall)       cmd_uninstall ;;
  --install-daemon)  cmd_install_daemon ;;
  --uninstall-daemon) cmd_uninstall_daemon ;;
  --selftest)        cmd_selftest ;;
  "")                nightly_run ;;
  *) echo "Usage: $0 [--test-notify|--install|--uninstall|--install-daemon|--uninstall-daemon|--selftest]" >&2; exit 2 ;;
esac
