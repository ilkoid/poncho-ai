#!/bin/bash
# pg-probe.sh — диагностика доступности PG 192.168.10.7:15432 (каждые 10 мин launchd-демоном).
# Пишет тройку: системный nc | bash /dev/tcp | homebrew pg_isready.
# Смысл: 05-06.09.2026 ловили блок macOS Local Network Privacy — под launchd не-Apple
# бинарники (pg_isready, go-run) получали отказ LAN-доступа при живом сервере, тогда как
# системные nc//dev/tcp проходили. Из Терминала работало всё. Root-демон блок не получает.
# Машинно-специфичен (пути/хост зашиты) — это диагностический скрипт, не библиотека.
LOG=/Users/ilkoid/dev/poncho-ai/logs/pg-probe.log
NC=$(nc -z -w 5 192.168.10.7 15432 >/dev/null 2>&1 && echo nc_OK || echo nc_FAIL)
TC=$(bash -c 'exec 3<>/dev/tcp/192.168.10.7/15432' 2>/dev/null && echo tcp_OK || echo tcp_FAIL)
PG=$(/opt/homebrew/opt/libpq/bin/pg_isready -h 192.168.10.7 -p 15432 -t 5)
echo "$(date '+%F %T') $NC | $TC | $PG" >> "$LOG"
