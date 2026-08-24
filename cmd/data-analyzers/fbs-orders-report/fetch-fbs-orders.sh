#!/usr/bin/env bash
# ONE-TIME snapshot fetch (2026-08-16): FBS assembly orders via curl.
# GET  /api/v3/orders         — orders created in window (createdAt, rid, no status)
# POST /api/v3/orders/status  — status query (supplierStatus/wbStatus), NOT a mutation
# Data → /tmp/fbs-snapshot-<date>/*.json ; no DB writes here.
set -euo pipefail

ENV_FILE="${ENV_FILE:-/Users/ilkoid/dev/poncho-ai/.env}"
DAYS="${DAYS:-90}"          # depth (API keeps 3 months)
WINDOW=30                   # max window per request (API limit)
LIMIT=1000                  # max page size
BASE="https://marketplace-api.wildberries.ru"
OUT="/tmp/fbs-snapshot-$(date +%Y%m%d-%H%M%S)"
PAUSE=1                     # req/sec, well under 300/min

# shellcheck disable=SC1090
set -a; source "$ENV_FILE"; set +a

: "${WB_API_CONTENT_KEY:?WB_API_CONTENT_KEY missing in .env}"
: "${WB_API_KEY:?WB_API_KEY missing in .env}"

mkdir -p "$OUT"
chmod 700 "$OUT"
HDR="$OUT/.auth.conf"       # key never on argv (ps-safe), deleted on exit
trap 'rm -f "$HDR"' EXIT
chmod 600 "$HDR" 2>/dev/null || true

KEY_TYPE=""
use_key() { # use_key <content|main>
  KEY_TYPE="$1"
  local k
  if [ "$1" = "content" ]; then k="$WB_API_CONTENT_KEY"; else k="$WB_API_KEY"; fi
  printf 'header = "Authorization: %s"\n' "$k" > "$HDR"
  chmod 600 "$HDR"
}

api_get() { # api_get <outfile> <url>  → http code
  curl -sS -K "$HDR" -o "$1" -w '%{http_code}' --max-time 120 "$2"
}
api_post() { # api_post <outfile> <url> <body>  → http code
  curl -sS -K "$HDR" -o "$1" -w '%{http_code}' --max-time 120 \
    -X POST -H 'Content-Type: application/json' -d "$3" "$2"
}

retry_401() { # retry_401 <got-code>
  [ "$1" = "401" ] || [ "$1" = "403" ]
}

NOW=$(date +%s)
START=$(( NOW - DAYS * 86400 ))
use_key "content"

echo "out: $OUT  depth: ${DAYS}d  start: $(date -u -r "$START" +%FT%TZ)"

# --- Phase 1: orders per 30-day window, cursor pagination ---
ids_file="$OUT/ids.txt"; : > "$ids_file"
total_orders=0
win=0
w_start=$START
while [ "$w_start" -lt "$NOW" ]; do
  w_end=$(( w_start + WINDOW * 86400 ))
  [ "$w_end" -gt "$NOW" ] && w_end=$NOW
  win=$(( win + 1 ))
  next=0; page=0
  while : ; do
    page=$(( page + 1 ))
    url="$BASE/api/v3/orders?dateFrom=$w_start&dateTo=$w_end&limit=$LIMIT&next=$next"
    f="$OUT/fbs-orders-w${win}-p${page}.json"
    code=$(api_get "$f" "$url")
    if retry_401 "$code" && [ "$KEY_TYPE" = "content" ]; then
      echo "  401/403 on content key → fallback WB_API_KEY"
      use_key "main"
      code=$(api_get "$f" "$url")
    fi
    if [ "$code" = "204" ]; then rm -f "$f"; break; fi
    [ "$code" = "200" ] || { echo "HTTP $code, url: $url"; head -c 300 "$f"; exit 1; }
    n=$(jq '.orders | length' "$f")
    total_orders=$(( total_orders + n ))
    jq -r '.orders[].id' "$f" >> "$ids_file"
    # cursor is a 19-digit int: extract from raw JSON (jq would lose precision)
    next=$(grep -o '"next":[0-9]\+' "$f" | head -1 | cut -d: -f2)
    echo "  w$win p$page: $n orders (next=${next:-end})"
    [ -z "$next" ] || [ "$next" = "0" ] || [ "$next" = "null" ] && break
    [ "$n" -eq 0 ] && break
    sleep "$PAUSE"
  done
  w_start=$w_end
done
echo "orders total: $total_orders, ids: $(wc -l < "$ids_file" | tr -d ' ')"

# --- Phase 2: statuses in chunks of 1000 ---
sort -u "$ids_file" -o "$ids_file"
n_ids=$(wc -l < "$ids_file" | tr -d ' ')
chunk=0; i=0
while [ "$i" -lt "$n_ids" ]; do
  chunk=$(( chunk + 1 ))
  body=$(sed -n "$(( i + 1 )),$(( i + 1000 ))p" "$ids_file" | jq -Rs 'split("\n") | map(select(length>0) | tonumber) | {orders: .}')
  f="$OUT/fbs-status-${chunk}.json"
  code=$(api_post "$f" "$BASE/api/v3/orders/status" "$body")
  if retry_401 "$code" && [ "$KEY_TYPE" = "content" ]; then
    echo "  401/403 on content key → fallback WB_API_KEY"
    use_key "main"
    code=$(api_post "$f" "$BASE/api/v3/orders/status" "$body")
  fi
  [ "$code" = "200" ] || { echo "HTTP $code on status chunk $chunk"; head -c 300 "$f"; exit 1; }
  echo "  status chunk $chunk: $(jq '.orders | length' "$f") rows"
  i=$(( i + 1000 ))
  [ "$i" -lt "$n_ids" ] && sleep "$PAUSE"
done

echo "DONE: $OUT  (orders=$total_orders, status_chunks=$chunk)"
