#!/bin/bash
# WB Full Data Refresh — unified dual-backend pipeline (SQLite + PostgreSQL)
#
# Usage: bash download-all.sh [days]
#   days  - override --days for downloaders that support it (default: from config.yaml)
#
# Runs all 20 v2 downloaders twice:
#   Pass 1 → SQLite (/var/db/wb-sales.db)
#   Pass 2 → PostgreSQL (wb_data_prod @ 192.168.10.7:15432)
#
# Phases ordered by speed: fast catalog → core sales → stock → advertising → slow analytics
# All configs in cmd/.configs/download-all/
#
# Downloader registry: DOWNLOADERS array at the bottom of this file.
# To add a new downloader: append one line to the array, following the format.

PONCHO="$(cd "$(dirname "$0")" && pwd)"
CONFIGS="$PONCHO/cmd/.configs/download-all"

# Lockfile: prevent concurrent runs (covers both passes)
LOCKFILE="$PONCHO/.download-all.lock"
exec 200>"${LOCKFILE}"
if ! flock -w 300 200; then
    echo "SKIP: another download process is running (lock: ${LOCKFILE})"
    exit 1
fi

DAYS="${1:-}"

echo "==========================================="
echo "  WB Full Data Download (dual-backend)"
echo "==========================================="
echo "Root dir:    $PONCHO"
echo "Configs:     $CONFIGS"
echo "Pass 1 DB:   /var/db/wb-sales.db (SQLite)"
echo "Pass 2 DB:   wb_data_prod (PostgreSQL 192.168.10.7:15432)"
echo "Days override: ${DAYS:-none (from config)}"
echo "Started:     $(date '+%Y-%m-%d %H:%M:%S')"
echo "==========================================="
START=$SECONDS

# ── Phase labels ──────────────────────────────────────────────────────
PHASE_NAMES=(
    ""
    "Catalog (cards, prices, 1C/PIM)"
    "Feedbacks"
    "Sales & Revenue"
    "Stock & Logistics"
    "Advertising (campaigns, promotion-v2)"
    "Analytics (funnel, funnel-agg, funnel-csv, search-vis, penalties, whremains)"
)

# ── Downloader registry ──────────────────────────────────────────────
# Format: "PHASE:DIR:CONFIG_BASE:FLAG_TYPE"
#   PHASE       — 1-6
#   DIR         — directory under cmd/data-downloaders/
#   CONFIG_BASE — config filename without .yaml and without -PG suffix
#   FLAG_TYPE   — none (no extra flags)
#               | days  (optional --days=$DAYS)
#               | today (--date $(date +%Y-%m-%d))
DOWNLOADERS=(
    # Phase 1: Catalog
    "1:download-wb-cards-v2:download-wb-cards-v2:none"
    "1:download-wb-prices-v2:download-wb-prices:none"
    "1:download-1c-data-v2:download-1c-data-v2:none"
    "1:download-1c-rests-v2:download-1c-rests:none"

    # Phase 2: Feedbacks
    "2:download-wb-feedbacks-v2:download-wb-feedbacks:days"

    # Phase 3: Sales & Revenue
    "3:download-wb-orders-v2:download-wb-orders:none"
    "3:download-wb-opsales-v2:download-wb-opsales:none"
    "3:download-wb-sales-v2:download-wb-sales:days"
    "3:download-wb-region-sales-v2:download-wb-region-sales-v2:days"

    # Phase 4: Stock & Logistics
    "4:download-wb-stocks-v2:download-wb-stocks-v2:today"
    "4:download-wb-stock-history-v2:download-wb-stock-history:days"
    "4:download-wb-supplies-v2:download-wb-supplies:days"

    # Phase 5: Advertising
    "5:download-wb-campaigns-v2:download-wb-campaigns-v2:none"
    "5:download-wb-promotion-v2:download-wb-promotion-v2:days"

    # Phase 6: Analytics
    "6:download-wb-funnel-v2:download-wb-funnel-v2:days"
    "6:download-wb-funnel-agg-v2:download-wb-funnel-agg:days"
    "6:download-wb-funnel-csv-v2:download-wb-funnel-csv-v2:days"
    "6:download-wb-search-vis-v2:download-wb-search-vis-v2:days"
    "6:download-wb-penalties-v2:download-wb-penalties-v2:none"
    "6:download-wb-whremains-v2:download-wb-whremains-v2:today"
)

# ── run_downloader: execute a single downloader ──────────────────────
run_downloader() {
    local dir="$1"
    local config_base="$2"
    local flag_type="$3"
    local backend="$4"
    local suffix="$5"

    local config_path="$CONFIGS/${config_base}${suffix}.yaml"

    # Build command
    local cmd="cd \"$PONCHO/cmd/data-downloaders/$dir\" && go run . --config \"$config_path\" --backend $backend"

    # Append extra flags based on type
    case "$flag_type" in
        none)  ;;
        days)  [ -n "$DAYS" ] && cmd="$cmd --days=$DAYS" ;;
        today) cmd="$cmd --date $(date +%Y-%m-%d)" ;;
    esac

    eval "$cmd" || exit $?
}

# ── run_pass: execute all downloaders for one backend ────────────────
run_pass() {
    local backend="$1"
    local suffix="$2"
    local db_label="$3"

    echo ""
    echo "═════════════════════════════════════════"
    echo "  Pass: $backend → $db_label"
    echo "═════════════════════════════════════════"

    local PASS_START=$SECONDS

    for phase in 1 2 3 4 5 6; do
        echo ""
        echo "── Phase $phase: ${PHASE_NAMES[$phase]} ──"
        local PHASE_START=$SECONDS

        for entry in "${DOWNLOADERS[@]}"; do
            IFS=':' read -r e_phase e_dir e_config e_flag <<< "$entry"
            if [ "$e_phase" -eq "$phase" ]; then
                run_downloader "$e_dir" "$e_config" "$e_flag" "$backend" "$suffix"
            fi
        done

        echo "  Phase $phase done in $(( SECONDS - PHASE_START ))s"
    done

    return $(( SECONDS - PASS_START ))
}

# ── Pass 1: SQLite ───────────────────────────────────────────────────
run_pass "sqlite" "" "/var/db/wb-sales.db"
SQLITE_ELAPSED=$?

# ── Pass 2: PostgreSQL ───────────────────────────────────────────────
run_pass "postgres" "-PG" "wb_data_prod (PostgreSQL @ 192.168.10.7:15432)"
PG_ELAPSED=$?

# ── Summary ──────────────────────────────────────────────────────────

ELAPSED=$(( SECONDS - START ))
MINS=$(( ELAPSED / 60 ))
SECS=$(( ELAPSED % 60 ))
SQLITE_MINS=$(( SQLITE_ELAPSED / 60 ))
SQLITE_SECS=$(( SQLITE_ELAPSED % 60 ))
PG_MINS=$(( PG_ELAPSED / 60 ))
PG_SECS=$(( PG_ELAPSED % 60 ))

echo ""
echo "==========================================="
echo "  All downloads completed"
echo "==========================================="
echo "  Pass 1 (SQLite):     ${SQLITE_MINS}m ${SQLITE_SECS}s"
echo "  Pass 2 (PostgreSQL): ${PG_MINS}m ${PG_SECS}s"
echo "  Total elapsed:       ${MINS}m ${SECS}s"
echo "  Finished: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""
echo "  20 v2 downloaders × 2 backends = 40 runs"
echo "==========================================="
