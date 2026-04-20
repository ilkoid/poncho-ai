#!/bin/bash
set -euo pipefail

# Daily analytics pipeline: git pull → download data → build MA snapshots
# Designed for cron on VPS. Uses pre-compiled binaries from bin/.

PROJECT="/home/ilkoid/go-workspace/src/poncho-ai"
LOCKFILE="${PROJECT}/.analytics.lock"
LOGDIR="${PROJECT}/logs"
BIN="${PROJECT}/bin"
CONFIG_DIR="${PROJECT}/cmd/.configs"
ANALYZER_CONFIG="${PROJECT}/cmd/data-analyzers/build-ma-sku-snapshots/config.vps.yaml"
ENV_FILE="${PROJECT}/.env"

# ── Logging ────────────────────────────────────────────────────
mkdir -p "${LOGDIR}"
TODAY=$(date +%Y-%m-%d)
LOGFILE="${LOGDIR}/analytics-${TODAY}.log"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "${LOGFILE}"; }

# ── Cleanup old logs (30 days) ────────────────────────────────
find "${LOGDIR}" -name '*.log' -mtime +30 -delete 2>/dev/null || true

# ── Lockfile (skip if already running) ─────────────────────────
exec 200>"${LOCKFILE}"
if ! flock -n 200; then
    log "SKIP: another instance is running (lock: ${LOCKFILE})"
    exit 0
fi

log "=== Daily analytics pipeline started ==="

# ── Load environment ───────────────────────────────────────────
if [[ -f "${ENV_FILE}" ]]; then
    # shellcheck source=/dev/null
    source "${ENV_FILE}"
    log "Loaded env from ${ENV_FILE}"
else
    log "ERROR: ${ENV_FILE} not found"
    exit 1
fi

cd "${PROJECT}"

# ── git pull (non-fatal) ──────────────────────────────────────
log "--- git pull ---"
if git pull 2>&1 | tee -a "${LOGFILE}"; then
    log "git pull OK"
else
    log "WARNING: git pull failed, continuing with existing code"
fi

START=$SECONDS

# ── Phase 1: Sales for MA computation ─────────────────────────
log "--- Sales ---"
"${BIN}/download-wb-sales" --no-service --config "${CONFIG_DIR}/download-wb-sales.yaml" 2>&1 | tee -a "${LOGFILE}"

# ── Phase 2: Cards + 1C/PIM catalog ──────────────────────────
log "--- Cards ---"
"${BIN}/download-wb-cards" --config "${CONFIG_DIR}/download-wb-cards.yaml" 2>&1 | tee -a "${LOGFILE}"

log "--- 1C/PIM catalog ---"
"${BIN}/download-1c-data" --config "${CONFIG_DIR}/download-1c-data.yaml" 2>&1 | tee -a "${LOGFILE}"

# ── Phase 3: Stock snapshots ──────────────────────────────────
log "--- Stock snapshots ---"
"${BIN}/download-wb-stocks" --date "$(date +%Y-%m-%d)" --config "${CONFIG_DIR}/download-wb-stocks.yaml" 2>&1 | tee -a "${LOGFILE}"

# ── Phase 4: Supplies ─────────────────────────────────────────
log "--- Supplies ---"
"${BIN}/download-wb-supplies" --config "${CONFIG_DIR}/download-wb-supplies.yaml" 2>&1 | tee -a "${LOGFILE}"

DOWNLOAD_ELAPSED=$(( SECONDS - START ))
log "=== Downloads completed in ${DOWNLOAD_ELAPSED}s ==="

# ── Phase 5: Build analytics ──────────────────────────────────
log "--- MA SKU Snapshots ---"
"${BIN}/build-ma-sku-snapshots" --config "${ANALYZER_CONFIG}" 2>&1 | tee -a "${LOGFILE}"

TOTAL_ELAPSED=$(( SECONDS - START ))
log "=== Pipeline completed in ${TOTAL_ELAPSED}s ==="
