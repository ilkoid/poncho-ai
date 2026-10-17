#!/bin/bash
# Утренний cron-прогон download-all-promo.sh (промо-блок) в ЛОКАЛЬНЫЙ PostgreSQL
# Mac Studio. Крон 05:30 — после ночного download-all.sh (03:00, ~50 мин) и волн
# финиширования авто-кампаний (02:30–02:45 массовая, ~05:15 вторая) — вселенная
# пар тощая, окно аг-контентаналитикс спокойно до ~08:00.
#
# Отличия от ручного запуска: PATH для go/psql (в cron его нет), dated-лог в
# logs/, caffeinate от сна Mac, ротация логов старше 30 дней.
# НЕ пересекается с ночным прогоном (03:00): разные скрипты, разные локи.

PONCHO="$(cd "$(dirname "$0")" && pwd)"
LOGDIR="$PONCHO/logs"
mkdir -p "$LOGDIR"
LOG="$LOGDIR/download-all-promo-$(date +%Y-%m-%d).log"

# cron PATH минимален (/usr/bin:/bin) — добавляем homebrew (go, pg_isready, psql)
export PATH="/opt/homebrew/bin:/opt/homebrew/opt/libpq/bin:$PATH"

{
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] === morning promo block → local PG (cron) ==="
  caffeinate -is bash "$PONCHO/download-all-promo.sh" "$@"
  rc=$?
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] === done, exit=$rc ==="
} >> "$LOG" 2>&1

find "$LOGDIR" -name 'download-all-promo-*.log' -mtime +30 -delete 2>/dev/null || true
