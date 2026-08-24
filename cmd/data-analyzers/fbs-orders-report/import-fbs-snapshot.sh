#!/usr/bin/env bash
# ONE-TIME import (2026-08-16): /tmp/fbs-snapshot-<date>/*.json → staging tables in PG.
#   public.fbs_orders         — from fbs-orders-*.json   (GET /api/v3/orders)
#   public.fbs_orders_status  — from fbs-status-*.json   (POST /api/v3/orders/status)
# Tables are disposable (DROP after analysis is fine).
# Usage: bash import-fbs-snapshot.sh /tmp/fbs-snapshot-20260816-XXXXXX
set -euo pipefail

DIR="${1:?usage: import-fbs-snapshot.sh /tmp/fbs-snapshot-<dir>}"
ENV_FILE="${ENV_FILE:-/Users/ilkoid/dev/poncho-ai/.env}"
PGHOST_="${PGHOST:-192.168.10.7}"
PGPORT_="${PGPORT:-15432}"
PGDB="${PGDB:-wb_data_prod}"
PGUSER_="${PGUSER:-postgres}"

# shellcheck disable=SC1090
set -a; source "$ENV_FILE"; set +a
: "${PG_PWD:?PG_PWD missing in .env}"

psql_exe() { PGPASSWORD="$PG_PWD" psql -h "$PGHOST_" -p "$PGPORT_" -U "$PGUSER_" -d "$PGDB" -v ON_ERROR_STOP=1 "$@"; }

echo "→ target: ${PGUSER_}@${PGHOST_}:${PGPORT_}/${PGDB}, source: $DIR"

psql_exe <<'SQL'
DROP TABLE IF EXISTS public.fbs_orders;
DROP TABLE IF EXISTS public.fbs_orders_status;
CREATE TABLE public.fbs_orders (
  id           BIGINT PRIMARY KEY,
  rid          TEXT NOT NULL,
  order_uid    TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT '',   -- RFC3339 UTC (напр. 2026-08-15T00:00:48Z)
  supply_id    TEXT NOT NULL DEFAULT '',
  warehouse_id BIGINT NOT NULL DEFAULT 0,  -- склад продавца
  office_id    BIGINT NOT NULL DEFAULT 0,  -- склад WB, к которому привязан склад продавца
  nm_id        BIGINT NOT NULL DEFAULT 0,
  article      TEXT NOT NULL DEFAULT '',
  chrt_id      BIGINT NOT NULL DEFAULT 0,
  price        BIGINT NOT NULL DEFAULT 0,  -- копейки
  is_zero_order BOOLEAN NOT NULL DEFAULT FALSE,
  downloaded_at TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')
);
CREATE INDEX idx_fbs_orders_rid  ON public.fbs_orders(rid);
CREATE INDEX idx_fbs_orders_created_at ON public.fbs_orders(created_at);
CREATE INDEX idx_fbs_orders_nm   ON public.fbs_orders(nm_id);

CREATE TABLE public.fbs_orders_status (
  order_id        BIGINT PRIMARY KEY,
  supplier_status TEXT NOT NULL DEFAULT '',
  wb_status       TEXT NOT NULL DEFAULT '',
  is_cancellable  BOOLEAN NOT NULL DEFAULT FALSE,
  downloaded_at   TEXT NOT NULL DEFAULT to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')
);
CREATE INDEX idx_fbs_status_supplier ON public.fbs_orders_status(supplier_status);
SQL

echo "→ orders: $(ls "$DIR"/fbs-orders-*.json 2>/dev/null | wc -l | tr -d ' ') files"
for f in "$DIR"/fbs-orders-*.json; do
  jq -r '.orders[] | [.id, .rid, (.orderUid // ""), (.createdAt // ""), (.supplyId // ""),
         (.warehouseId // 0), (.officeId // 0), (.nmId // 0), (.article // ""),
         (.chrtId // 0), (.price // 0), (.isZeroOrder // false)] | @tsv' "$f"
done > /tmp/fbs_orders.tsv

echo "→ status: $(ls "$DIR"/fbs-status-*.json 2>/dev/null | wc -l | tr -d ' ') files"
for f in "$DIR"/fbs-status-*.json; do
  jq -r '.orders[] | [.id, (.supplierStatus // ""), (.wbStatus // ""), (.isCancellable // false)] | @tsv' "$f"
done > /tmp/fbs_status.tsv

wc -l /tmp/fbs_orders.tsv /tmp/fbs_status.tsv

psql_exe -c "\copy public.fbs_orders(id,rid,order_uid,created_at,supply_id,warehouse_id,office_id,nm_id,article,chrt_id,price,is_zero_order) FROM '/tmp/fbs_orders.tsv' WITH (FORMAT text)"
psql_exe -c "\copy public.fbs_orders_status(order_id,supplier_status,wb_status,is_cancellable) FROM '/tmp/fbs_status.tsv' WITH (FORMAT text)"

psql_exe -c "SELECT 'fbs_orders' t, count(*) n, min(created_at) min_created, max(created_at) max_created FROM public.fbs_orders
             UNION ALL SELECT 'fbs_orders_status', count(*), min(supplier_status), max(supplier_status) FROM public.fbs_orders_status"
echo "DONE"
