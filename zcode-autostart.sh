#!/bin/bash
# zcode-autostart.sh — keepalive ZCode.app (LaunchAgent gui-домена, без sudo).
# Запускается агентом com.ilkoid.poncho.zcode-autostart: при логине (RunAtLoad)
# и далее каждые 5 мин. Если приложение закрылось (мигание света, самообновление,
# случайное закрытие) — открываем заново. Пара к нему — автовход macOS: без
# залогиненной сессии агент сам не стартует после перезагрузки.
#
# Проверка через `ps -o ucomm` + awk, НЕ pgrep и НЕ grep -x: на этой машине
# pgrep не видит главный процесс ZCode вовсе (только Electron-хелперов), а
# колонка ucomm в `ps ax` паддится ПРОБЕЛАМИ СПРАВА (grep -x ловит только
# «ZCode<17 пробелов>» вместо «ZCode») — проверено эмпирически 06.09.2026.
# awk сравнивает первое поле, паддинг ему безразличен.
#
# Выключить: launchctl unload ~/Library/LaunchAgents/com.ilkoid.poncho.zcode-autostart.plist
LOG=/Users/ilkoid/dev/poncho-ai/logs/zcode-autostart.log
if ! ps ax -o ucomm= | awk '$1=="ZCode"{f=1} END{exit !f}'; then
  open -a /Applications/ZCode.app 2>/dev/null
  echo "$(date '+%F %T') ZCode не был запущен → open" >> "$LOG"
fi
