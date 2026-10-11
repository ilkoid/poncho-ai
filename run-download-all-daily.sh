#!/bin/bash
# Ночной cron-прогон download-all.sh в ЛОКАЛЬНЫЙ PostgreSQL Mac Studio.
#
# Создан 2026-10-11, пока основной PG (192.168.10.7:15432) недоступен из локации.
# Локальный сервер: brew postgresql@18 (launchd, `brew services`), 127.0.0.1:5432,
# scram по TCP; PGHOST/PGPORT заданы в .env — download-all.sh подхватит их сам.
#
# Когда вернёмся на основной сервер:
#   1. закомментировать export PGHOST/PGPORT в .env (блок «Локальный PG на Mac Studio»);
#   2. закомментировать строку run-download-all-daily.sh в crontab (crontab -e);
#   3. догнать данные на основном сервере повторным прогоном download-all с окном дней.
#
# Отличия от ручного запуска: PATH для go/psql (в cron его нет), dated-лог в logs/,
# caffeinate от сна Mac, ротация логов старше 30 дней.

PONCHO="$(cd "$(dirname "$0")" && pwd)"
LOGDIR="$PONCHO/logs"
mkdir -p "$LOGDIR"
LOG="$LOGDIR/download-all-$(date +%Y-%m-%d).log"

# cron PATH минимален (/usr/bin:/bin) — добавляем homebrew (go, pg_isready, psql)
export PATH="/opt/homebrew/bin:/opt/homebrew/opt/libpq/bin:$PATH"

{
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] === nightly download-all → local PG (cron) ==="
  caffeinate -is bash "$PONCHO/download-all.sh" "$@"
  rc=$?
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] === done, exit=$rc ==="
} >> "$LOG" 2>&1

find "$LOGDIR" -name 'download-all-*.log' -mtime +30 -delete 2>/dev/null || true
