#!/bin/bash
# run-penalties-dims-fixer.sh — hourly cron wrapper for the МГХ penalties fixer.
#
# Runs AFTER download-wb-penalties-v2 has refreshed measurement_penalties.
# Stages confirmed penalties and applies the dimension fixes (idempotent: cards
# already matching the WB measurement are skipped). All output → daily log.
#
# Install in crontab (off-minute, after the penalties downloader). Example:
#   37 * * * * /home/ilkoid/go-workspace/src/poncho-ai/cmd/fix-utilities/fix-penalties-dims/run-penalties-dims-fixer.sh
#
# Env (cron has a minimal environment — source an env file):
#   WB_API_ANALYTICS_AND_PROMO_KEY (or WB_API_KEY)
#   PGHOST / PGPORT / PGUSER / PG_PWD / PGDATABASE
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CFG="$SCRIPT_DIR/config.yaml"
LOGDIR="$SCRIPT_DIR/logs"
mkdir -p "$LOGDIR"
LOG="$LOGDIR/run-$(date +%Y-%m-%d).log"

# Non-blocking lock — skip if a previous run is still going (e.g. overrun).
LOCK="$REPO_ROOT/.penalties-dims-fixer.lock"
exec 9>"$LOCK"
if ! flock -n 9; then
  echo "[$(date '+%F %T')] SKIP: another instance is running" >>"$LOG"
  exit 0
fi

# Source secrets/env for cron (create .env.wb at repo root with the vars above).
if [ -f "$REPO_ROOT/.env.wb" ]; then
  set -a
  # shellcheck disable=SC1090
  source "$REPO_ROOT/.env.wb"
  set +a
fi

echo "[$(date '+%F %T')] === penalties-dims fixer start ===" >>"$LOG"

# --auto = stage + apply. For a FIRST-TIME dry run, add --dry-run here and review.
cd "$REPO_ROOT"
go run "$SCRIPT_DIR" --auto --config "$CFG" >>"$LOG" 2>&1

echo "[$(date '+%F %T')] === penalties-dims fixer done ===" >>"$LOG"
