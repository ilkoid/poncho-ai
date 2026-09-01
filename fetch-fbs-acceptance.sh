#!/bin/bash
# Moment Refresh — приёмка на СС + классические снепшоты (остатки 1С/WB)
#
# Usage: bash fetch-fbs-acceptance.sh
#
# Два блока:
#
#   Block 1 «Классические снепшоттеры» — текущее состояние без истории
#   (тот же активный набор, что в download-snapshots.sh, Pass 1 PG):
#     - download-1c-rests-v2        — остатки 1С
#     - download-wb-stocks-v2       — остатки WB по складам
#     - download-wb-stock-products-v2 — остатки WB по номенклатурам
#     - download-wb-whremains-v2    — остатки по коробам/складам WB
#   Пропущенный прогон = дыра в истории снапшотов навсегда.
#
#   Block 2 «FBS moment» — поставки + текущие состояния заказов:
#     - фаза 3 поставки — ПРИЁМКА НА СС (scan_dt, «дата сканирования поставки
#       или первого заказа»): полный список ~сотен строк, каждый прогон
#       дообновляет открытые поставки
#     - фаза 2 статусы — снапшот текущего состояния: журнал переходов
#       fbs_orders_status_log имеет гранулярность = частоту запусков
#     - фаза 4 лента — несёт ТЕКУЩИЙ статус заказа + updated_at (дату этого
#       статуса): период запроса фильтруется по дате текущего статуса, поэтому
#       промежуточные состояния между прогонами запросить позже нельзя — заказ
#       «уехал» из старого окна; частота прогонов = гранулярность событий
#     - фаза 1 задания — историчны (по дате создания); узкое окно ORDERS_DAYS
#       нужно только для свежего supply_id — связь «заказ → поставка» для лага
#       приёмки живёт в fbs_orders
#
# Длительность ≈ 5–8 мин при FEED_DAYS=2 (лента идёт 1 страницу/мин).
# Ночной download-all.sh остаётся каноном; download-snapshots.sh — тем же
# набором снепшотов без FBS-части. Этот скрипт — их внутридневное дополнение.
#
# Переменные окружения (можно переопределять при запуске):
#   ORDERS_DAYS=3   окно фазы заданий (свежий supply_id)
#   STATUS_DAYS=2   окно фазы статусов (незакрытые задания качаются всегда)
#   FEED_DAYS=2     глубина ленты; держи ≥ интервала между прогонами,
#                   иначе события между прогонами проскочат мимо окна
#
# Пример: FEED_DAYS=3 bash fetch-fbs-acceptance.sh

export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"

ORDERS_DAYS="${ORDERS_DAYS:-3}"
STATUS_DAYS="${STATUS_DAYS:-2}"
FEED_DAYS="${FEED_DAYS:-2}"

# ── Load .env if present (local-run support; как download-all.sh) ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Lock: свой + не пересекаться с ночным download-all (двойные записи в те же таблицы) ──
if [ -d "$PONCHO/.download-all.lock" ]; then
  echo "SKIP: идёт download-all.sh — полный прогон уже качает всё нужное" >&2
  exit 0
fi
LOCKDIR="$PONCHO/.fetch-fbs-acceptance.lock"
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  echo "SKIP: другой fetch-fbs-acceptance уже идёт (lock: $LOCKDIR)" >&2
  exit 0
fi
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable ──
if ! nc -z -w 5 "$PGHOST" "$PGPORT" 2>/dev/null; then
  echo "FAIL: PostgreSQL $PGHOST:$PGPORT недоступен. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env" >&2
  exit 1
fi

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
FAILED=()
RUN_COUNT=0

# run — обёртка над "go run <pkg> …". Имя утилиты = basename каталога из $3.
run() {
  local name; name="$(basename "$3")"
  RUN_COUNT=$(( RUN_COUNT + 1 ))
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    echo "⚠️  FAIL: $name (exit $rc)" >&2
  fi
}

START=$SECONDS
echo "═══════ Moment Refresh: снепшоты + приёмка на СС ═══════"

# ── Block 1: классические снепшоттеры (как download-snapshots.sh, Pass 1 PG) ──
echo "── Block 1/2: снепшоты остатков (1С + WB) ──"

run go run "$PONCHO/cmd/data-downloaders/download-1c-rests-v2" --config "$C/download-1c-rests-v2-PG.yaml" --backend postgres
run go run "$PONCHO/cmd/data-downloaders/download-wb-stocks-v2" --config "$C/download-wb-stocks-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-stock-products-v2" --config "$C/download-wb-stock-products-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)
run go run "$PONCHO/cmd/data-downloaders/download-wb-whremains-v2" --config "$C/download-wb-whremains-v2-PG.yaml" --backend postgres --date $(date +%Y-%m-%d)

# ── Block 2: FBS moment — поставки (приёмка на СС) + текущие состояния ──
echo "── Block 2/2: FBS — поставки (приёмка на СС) + статусы + лента ──"
echo "Окна: задания ${ORDERS_DAYS}д · статусы ${STATUS_DAYS}д · лента ${FEED_DAYS}д"

run go run "$PONCHO/cmd/data-downloaders/download-wb-fbs-orders-v2" \
  --config "$C/download-wb-fbs-orders-PG.yaml" \
  --backend postgres \
  --days "$ORDERS_DAYS" \
  --status-window-days "$STATUS_DAYS" \
  --feed-days "$FEED_DAYS"

# ── Опционально: свежий HTML-дашборд с секцией «Приёмка на СС» (~25 с) ──
# Раскомментируй, если нужен файл сразу после каждого прогона:
# run go run "$PONCHO/cmd/data-analyzers/fbs-funnel-report" \
#   --config "$PONCHO/cmd/data-analyzers/fbs-funnel-report/config.yaml" \
#   --html auto

# ── Summary ──
ELAPSED=$(( SECONDS - START ))
echo ""
echo "═══════ Summary: ${RUN_COUNT} утилит за ${ELAPSED}s ═══════"
if [ "${#FAILED[@]}" -gt 0 ]; then
  echo "⚠️  Упали: ${FAILED[*]}" >&2
  exit 1
fi
echo "Все ок. Лента = ${FEED_DAYS}д × ~1 стр/мин — главное слагаемое времени."
