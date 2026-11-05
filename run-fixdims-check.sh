#!/usr/bin/env bash
# run-fixdims-check.sh — разовая пред-полётная проверка рефакторинга fill-dims (05.11).
# НЕ кроновый: запускается вручную перед первой ночью на новом коде, удалить после
# стабилизации. WB не трогает (--apply --dry-run); stage пересоздаёт только локальный
# staging в ~/dev/fix-dims.db (ночной крон и так делает DELETE+INSERT).
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
FIX="$REPO/cmd/fix-utilities/fix-card-dimensions-v1.5"
CFG="$FIX/config-years-25-26.yaml"
DB="$HOME/dev/fix-dims.db"

# cron PATH минимален — homebrew (go) первым, как в fill-dims.sh
export PATH="/opt/homebrew/bin:$PATH"
set -a; source "$REPO/.env"; set +a

echo '=== 1. build + vet ==='
go build "$FIX" && go vet "$FIX"

echo '=== 2. тесты утилиты ==='
go test "$FIX"

echo '=== 3. stage (локальный кеш, staging пересоздаётся) ==='
go run "$FIX" --stage --config "$CFG"

echo '=== 4. dry-run apply (WB не зовётся) ==='
go run "$FIX" --apply --dry-run --config "$CFG"

echo '=== 5. статусы staging после dry-run ==='
sqlite3 "$DB" "SELECT status, COUNT(*) FROM fix_card_dimensions_staging GROUP BY status;"

echo '=== 6. причины skip (топ-10) ==='
sqlite3 "$DB" "SELECT substr(error_msg,1,80), COUNT(*) FROM fix_card_dimensions_staging WHERE status='skipped_hollow' GROUP BY 1 ORDER BY 2 DESC LIMIT 10;"

echo '=== ГОТОВО. Реальный apply — вручную: bash fill-dims.sh ==='
