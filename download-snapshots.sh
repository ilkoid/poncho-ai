#!/bin/bash
# WB Snapshot Refresh — dual-backend (PostgreSQL → SQLite)
#
# Usage: bash download-snapshots.sh
#
# Только снепшот-утилиты (текущее состояние, без исторических данных).
# Запускается быстрее полного download-all.sh.
#
# Как пользоваться:
#   1. Закомментируй строку с утилитой → она пропускается
#   2. Раскомментируй → снова работает
#
# Два прогона: сначала PostgreSQL, потом SQLite.
# Чтобы запустить только один бэкенд — закомментируй весь блок Pass 1 или Pass 2.

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"

START=$SECONDS
export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

###############################################################################
#  PASS 1: PostgreSQL
###############################################################################

PG_START=$SECONDS
echo ""
echo "═══════  Pass 1: PostgreSQL  ═══════"

# ── Catalog ──
echo "── Catalog ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-PG.yaml" --backend postgres

# ── Stock & Penalties ──
echo "── Stock & Penalties ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2-PG.yaml" --backend postgres
go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)

PG_ELAPSED=$(( SECONDS - PG_START ))

###############################################################################
#  PASS 2: SQLite
###############################################################################

SQLITE_START=$SECONDS
echo ""
echo "═══════  Pass 2: SQLite  ═══════"

# ── Catalog ──
echo "── Catalog ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-cards-v2" --config "$C/download-wb-cards-v2.yaml" --backend sqlite
go run "$PONCHO/cmd/data-downloaders/download-wb-prices-v2" --config "$C/download-wb-prices.yaml" --backend sqlite
go run "$PONCHO/cmd/data-downloaders/download-1c-data-v2" --config "$C/download-1c-data-v2.yaml" --backend sqlite
go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests.yaml" --backend sqlite

# ── Stock & Penalties ──
echo "── Stock & Penalties ──"

go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products.yaml" --backend sqlite --date $(date +%Y-%m-%d)
go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$C/download-wb-penalties-v2.yaml" --backend sqlite
go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2.yaml" --backend sqlite --date $(date +%Y-%m-%d)

SQLITE_ELAPSED=$(( SECONDS - SQLITE_START ))

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
echo ""
echo "PG:     $((PG_ELAPSED / 60))m $((PG_ELAPSED % 60))s"
echo "SQLite: $((SQLITE_ELAPSED / 60))m $((SQLITE_ELAPSED % 60))s"
echo "Total:  $((TOTAL / 60))m $((TOTAL % 60))s"
