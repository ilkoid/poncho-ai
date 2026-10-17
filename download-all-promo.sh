#!/bin/bash
# WB Промо-блок — штормовые ручки аг-рекламы/аг-аналитики, вынесенные из ночного
# download-all.sh в утренний слот (крон 05:30 через run-download-all-promo-daily.sh).
#
# Состав: campaigns, promotion, funnel-agg, funnel-csv, search-vis + финальный
# ANALYZE. Всё остальное (контент/1С/продажи/остатки/penalties/item-rating/
# whremains) остаётся в ночном download-all.sh (крон 03:00).
#
# Почему утром: после волн финиширования авто-кампаний (массовый флип 02:30–02:45,
# вторая волна ~05:15) вселенная пар тощая (~17 тыс. против ~110 тыс. ночью), а окно
# 05:30–08:00 — спокойное для аг-контентаналитикс до старта внешних сервисов (~08:00,
# купленные интеграции работают с нашим ключом 08:00–24:00). Зависимость: promotion
# читает campaign_products свежего campaigns — порядок фаз не менять.
#
# Usage: bash download-all-promo.sh [days]
# Вручную на Mac: caffeinate -is bash download-all-promo.sh (блок ~3ч20м)
#
# Summary в конце перечисляет упавшие утилиты; код выхода = 1, если хоть одна упала.

export PGHOST="${PGHOST:-192.168.10.7}"
export PGPORT="${PGPORT:-15432}"
export PGUSER="${PGUSER:-postgres}"

PONCHO="$(cd "$(dirname "$0")" && pwd)"
C="$PONCHO/cmd/.configs/download-all"
DAYS="${1:-}"

# go run <пакет> требует cwd внутри модуля (cron зовёт по абсолютному пути)
cd "$PONCHO" || { echo "FAIL: не могу зайти в $PONCHO" >&2; exit 1; }

# ── Load .env if present (local-run support) ──
if [ -f "$PONCHO/.env" ]; then
  set -a
  . "$PONCHO/.env"
  set +a
fi

# ── Single-instance lock: СВОЙ каталог (не пересекаться с ночным .download-all.lock,
#    его читают fetch-fbs-acceptance и др.); протухший (>4ч без прогресса) — убрать. ──
LOCKDIR="$PONCHO/.download-all-promo.lock"
find "$PONCHO" -maxdepth 1 -name '.download-all-promo.lock' -type d -mmin +240 -exec rmdir {} \; 2>/dev/null
if ! mkdir "$LOCKDIR" 2>/dev/null; then
  echo "SKIP: другой промо-прогон уже идёт (lock: $LOCKDIR)" >&2
  exit 0
fi
touch "$LOCKDIR"   # mtime = момент старта; шаги ниже обновляют его
trap 'rmdir "$LOCKDIR" 2>/dev/null' EXIT INT TERM

# ── Fail fast if PG is unreachable ──
PG_HOST="${PGHOST}"; PG_PORT="${PGPORT}"
PG_ISREADY="$(command -v pg_isready || echo /opt/homebrew/opt/libpq/bin/pg_isready)"
if [ -x "$PG_ISREADY" ]; then
  "$PG_ISREADY" -h "$PG_HOST" -p "$PG_PORT" -t 5 >/dev/null 2>&1 || PG_DOWN=1
else
  nc -z -w 5 "$PG_HOST" "$PG_PORT" 2>/dev/null || PG_DOWN=1
fi
if [ "${PG_DOWN:-0}" = "1" ]; then
  echo "FAIL: PostgreSQL $PG_HOST:$PG_PORT не отвечает. Проверь PGHOST/PGPORT/PG_PWD в $PONCHO/.env" >&2
  exit 1
fi

# ── Failure tracking: Summary в конце перечисляет упавшие утилиты ──
FAILED=()
RUN_COUNT=0

# run — обёртка над "go run <pkg> …". Имя утилиты = basename каталога из $3.
run() {
  local name; name="$(basename "$3")"
  RUN_COUNT=$((RUN_COUNT + 1))
  touch "$LOCKDIR" 2>/dev/null || true
  "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("$name (exit $rc)")
    echo "⚠️  FAIL: $name (exit $rc)" >&2
  fi
  return "$rc"
}

# maint <group> — целевая оптимизация таблиц утилиты ДО её запуска
# (группы и тиры — в pg-maintenance-PG.yaml). Падение НЕ блокирует загрузку.
maint() {
  RUN_COUNT=$((RUN_COUNT + 1))
  touch "$LOCKDIR" 2>/dev/null || true
  go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --group "$1"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    FAILED+=("pg-maintenance[$1] (exit $rc)")
    echo "⚠️  FAIL: pg-maintenance[$1] (exit $rc)" >&2
  fi
  return "$rc"
}

START=$SECONDS
PG_START=$SECONDS
echo ""
echo "═══════  WB Promo Block (PostgreSQL)  ═══════"

# ── Phase 1: Campaigns — номенклатура кампаний = вселенная пар для promotion;
#    утренний снимок = тощая вселенная (после волн флипов) ──
echo "── Phase 1: Campaigns ──"
maint campaigns
run go run "$PONCHO/cmd/data-downloaders/download-wb-campaigns-v2" --config "$C/download-wb-campaigns-v2-PG.yaml" --backend postgres

# ── Phase 2: Promotion (normquery stats/list/bids/minus, min-bids, бюджеты) —
#    аг-адверт; ретраи «подсказка+ε» + rescue уже в конфиге ──
echo "── Phase 2: Promotion ──"
maint promotion
run go run "$PONCHO/cmd/data-downloaders/download-wb-promotion-v2" --config "$C/download-wb-promotion-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 3: Funnel (агрегатная воронка + недельный CSV-отчёт) ──
echo "── Phase 3: Funnel ──"
maint funnel-agg
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-agg-v2" --config "$C/download-wb-funnel-agg-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}
maint funnel-csv
run go run "$PONCHO/cmd/data-downloaders/download-wb-funnel-csv-v2" --config "$C/download-wb-funnel-csv-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 4: Search visibility — аг-контентаналитикс; до ~08:00 ведро спокойно,
#    дальше шторм внешних сервисов — схема «подсказка+ε» + rescue держат ──
echo "── Phase 4: Search visibility ──"
maint search-vis
run go run "$PONCHO/cmd/data-downloaders/download-wb-search-vis-v2" --config "$C/download-wb-search-vis-v2-PG.yaml" --backend postgres ${DAYS:+--days=$DAYS}

# ── Phase 5: PG Maintenance — ANALYZE всех таблиц (свежие статистики к рабочему дню) ──
echo "── Phase 5: PG Maintenance (ANALYZE all) ──"
run go run "$PONCHO/cmd/data-maintenance/pg-maintenance" --config "$C/pg-maintenance-PG.yaml" --analyze-only

PG_ELAPSED=$(( SECONDS - PG_START ))

###############################################################################
#  Summary
###############################################################################

TOTAL=$(( SECONDS - START ))
echo ""
echo "═══════  Summary  ═══════"
if [ "${#FAILED[@]}" -eq 0 ]; then
  echo "✔ Все утилиты завершились успешно ($RUN_COUNT/$RUN_COUNT)"
else
  echo "✗ Не выполнились (${#FAILED[@]} из $RUN_COUNT):"
  printf '  ✗ %s\n' "${FAILED[@]}"
fi
echo "PG:     $((PG_ELAPSED / 60))m $((PG_ELAPSED % 60))s"
[ "${#FAILED[@]}" -eq 0 ] || exit 1
