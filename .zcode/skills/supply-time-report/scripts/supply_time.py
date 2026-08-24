#!/usr/bin/env python3
"""Анализ среднего времени поставок WB на конкретные склады.

Primary-метрика (см. references/methodology.md):
    delivery_time_days = DATE(updated_date) - DATE(create_date)
    WHERE status_id = 5 (Принято) AND ready_for_sale_quantity > 0

`updated_date` фиксирует момент, когда WB записал поставку как принятую и
готовую к продаже. `fact_date` не используется (часто равен create_date и
занижен относительно реального появления товара в продаже).

Cross-check через stocks_daily_warehouses (--validate-stocks) — опциональный,
только для складов, описанных в warehouse_aliases.yaml (25.7% поставок не
сматчатся из-за рассинхрона имён).

Все запросы — read-only SELECT. Никаких writes.

Usage:
    python3 supply_time.py                          # отчёт по всем складам
    python3 supply_time.py --warehouse "Электросталь"
    python3 supply_time.py --since 2026-06-01 --until 2026-07-01
    python3 supply_time.py --drill-down "Электросталь" --format markdown
    python3 supply_time.py --validate-stocks        # cross-check со стоками
    python3 supply_time.py --sort-by p90 --format csv
"""

from __future__ import annotations

import argparse
import csv
import io
import os
import statistics
import sys
from dataclasses import dataclass, field
from datetime import date, datetime
from typing import Iterable

import yaml
from tabulate import tabulate

try:
    import numpy as np
except ImportError:  # pragma: no cover
    np = None  # noqa: E305

# Делаем `from db import ...` рабочим при запуске из любой CWD:
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
if SCRIPT_DIR not in sys.path:
    sys.path.insert(0, SCRIPT_DIR)

from db import connect, log_dsn, resolve_dsn  # noqa: E402

ALIASES_FILE = os.path.join(SCRIPT_DIR, "warehouse_aliases.yaml")

# status_id из Swagger enum (07-orders-fbw.yaml, models.HandySupplyStatus).
STATUS_NAMES = {
    1: "Не запланировано",
    2: "Запланировано",
    3: "Отгрузка разрешена",
    4: "Идёт приёмка",
    5: "Принято",
    6: "Отгружено на воротах",
}
COMPLETED_STATUS = 5  # Принято


# ──────────────────────────── data classes ────────────────────────────


@dataclass
class Supply:
    supply_id: int
    preorder_id: int
    create_d: date
    updated_d: date
    warehouse_name: str
    ready_for_sale_quantity: int | None
    accepted_quantity: int | None
    # Вычисляется лениво (lag может уйти в отрицательное при сбое API).
    lag_days: int = field(init=False)

    def __post_init__(self) -> None:
        self.lag_days = (self.updated_d - self.create_d).days


@dataclass
class WarehouseStats:
    warehouse_name: str
    n_supplies: int
    delivery_times: list[int]  # список lag_days по каждой поставке

    # — статистики (считаются лениво через @property) —

    @property
    def mean(self) -> float:
        return statistics.fmean(self.delivery_times) if self.delivery_times else float("nan")

    @property
    def median(self) -> float:
        return statistics.median(self.delivery_times) if self.delivery_times else float("nan")

    def percentile(self, p: float) -> float:
        """Перцентиль через numpy (linear interpolation между ближайшими).
        Если numpy недоступен — fallback через упорядоченный slice.
        """
        if not self.delivery_times:
            return float("nan")
        if np is not None:
            return float(np.percentile(self.delivery_times, p))
        # Fallback (метод nearest-rank без интерполяции):
        sorted_lags = sorted(self.delivery_times)
        idx = max(0, min(len(sorted_lags) - 1, int(round(p / 100 * (len(sorted_lags) - 1)))))
        return float(sorted_lags[idx])

    @property
    def p25(self) -> float:
        return self.percentile(25)

    @property
    def p75(self) -> float:
        return self.percentile(75)

    @property
    def p90(self) -> float:
        return self.percentile(90)

    @property
    def min_lag(self) -> int:
        return min(self.delivery_times) if self.delivery_times else 0

    @property
    def max_lag(self) -> int:
        return max(self.delivery_times) if self.delivery_times else 0


# ──────────────────────────── queries ────────────────────────────


def check_freshness(conn) -> tuple[date | None, int | None]:
    """Возвращает (last_supply_date, lag_days от сегодня). Алерт при lag>3."""
    sql = """
        SELECT MAX(substring(create_date from 1 for 10)::date) AS last_date,
               CURRENT_DATE - MAX(substring(create_date from 1 for 10)::date) AS lag_days
        FROM supplies
        WHERE status_id = 5 AND ready_for_sale_quantity > 0;
    """
    with conn.cursor() as cur:
        cur.execute(sql)
        row = cur.fetchone()
    if not row:
        return None, None
    # CURRENT_DATE - date возвращает int (количество дней), не timedelta.
    lag = row["lag_days"]
    return row["last_date"], (int(lag) if lag is not None else None)


def fetch_supplies(
    conn,
    warehouse: str | None = None,
    since: date | None = None,
    until: date | None = None,
    include_in_progress: bool = False,
    only_manual: bool = True,
) -> list[Supply]:
    """Загружает завершённые поставки (status_id=5, ready_for_sale>0).

    `include_in_progress=True` — добавляет статусы 1-4 (без лага — skipped).
    `only_manual=True` (дефолт) — только ручные поставки менеджера
    (`box_type_id > 0`, т.е. короба/монопаллеты/суперсейф). Отсекает
    автоматические WB-поставки `box_type_id=0` (виртуальные: «Допринято»,
    «Обезличка», «QR-приёмка» и т.д.), которые WB генерирует сам ночью
    (например, при доприёме возвратов из ПВЗ). Для бизнес-задачи «время от
    решения менеджера поставить до доступности» нужен только ручной поток.
    """
    # Используем substring(...) для извлечения YYYY-MM-DD из ISO-8601 text,
    # затем ::date. Это стабильный паттерн (см. sales-trend-report).
    sql = """
        SELECT supply_id, preorder_id,
               substring(create_date from 1 for 10)::date  AS create_d,
               substring(updated_date from 1 for 10)::date AS updated_d,
               warehouse_name,
               ready_for_sale_quantity,
               accepted_quantity
        FROM supplies
        WHERE status_id = ANY(%s)
          AND create_date IS NOT NULL AND updated_date IS NOT NULL
          AND warehouse_name IS NOT NULL
          AND (%s::text IS NULL OR warehouse_name = %s)
          AND (%s::date IS NULL OR substring(create_date from 1 for 10)::date >= %s)
          AND (%s::date IS NULL OR substring(create_date from 1 for 10)::date <= %s)
    """
    statuses = [COMPLETED_STATUS] if not include_in_progress else [1, 2, 3, 4, COMPLETED_STATUS]
    # Для не-завершённых статусов ready_for_sale_quantity может быть NULL/0,
    # лаг по updated_date всё равно осмысленный ("сколько ждём от создания").
    if not include_in_progress:
        sql += " AND ready_for_sale_quantity > 0"
    if only_manual:
        # box_type_id: 0=виртуальная (авто, WB), 1/2=короба, 5=монопаллеты,
        # 6=суперсейф. Только box_type_id > 0 — это ручные поставки менеджера.
        # Этим отсекается virtual_type_id=5 «Допринято» (120/196 в наших данных)
        # и все остальные автоматические виртуальные поставки.
        sql += " AND box_type_id > 0"
    sql += ";"
    with conn.cursor() as cur:
        cur.execute(
            sql,
            (statuses, warehouse, warehouse, since, since, until, until),
        )
        rows = cur.fetchall()
    supplies = []
    for r in rows:
        try:
            supplies.append(
                Supply(
                    supply_id=r["supply_id"],
                    preorder_id=r["preorder_id"],
                    create_d=r["create_d"],
                    updated_d=r["updated_d"],
                    warehouse_name=r["warehouse_name"],
                    ready_for_sale_quantity=r["ready_for_sale_quantity"],
                    accepted_quantity=r["accepted_quantity"],
                )
            )
        except (TypeError, ValueError) as e:
            print(f"[warn] пропускаю строку supply_id={r.get('supply_id')}: {e}", file=sys.stderr)
    return supplies


def group_by_warehouse(supplies: Iterable[Supply]) -> list[WarehouseStats]:
    """Группирует поставки по складу, возвращает отсортированный список."""
    groups: dict[str, list[int]] = {}
    for s in supplies:
        groups.setdefault(s.warehouse_name, []).append(s.lag_days)
    stats = [
        WarehouseStats(wh, len(lags), lags)
        for wh, lags in groups.items()
    ]
    return stats


def sort_stats(stats: list[WarehouseStats], by: str) -> list[WarehouseStats]:
    """Сортирует по выбранной метрике (по убыванию, кроме mean/median/p90 —
    по возрастанию времени, чтобы 'быстрые' склады были сверху).
    """
    key_map = {
        "count": lambda s: s.n_supplies,
        "mean": lambda s: s.mean,
        "median": lambda s: s.median,
        "p90": lambda s: s.p90,
    }
    key = key_map.get(by, key_map["count"])
    reverse = by == "count"
    return sorted(stats, key=key, reverse=reverse)


# ──────────────────────────── cross-check stocks ────────────────────────────


def load_aliases(path: str = ALIASES_FILE) -> dict[str, list[str]]:
    if not os.path.exists(path):
        return {}
    with open(path, "r", encoding="utf-8") as f:
        data = yaml.safe_load(f) or {}
    # Каноничное имя включаем в список кандидатов на всякий случай.
    return {k: [k] + list(v) for k, v in data.items()}


def crosscheck_stocks(conn, supplies: list[Supply], aliases: dict[str, list[str]]) -> dict[int, date | None]:
    """Для каждой поставки — эмпирическая дата появления её товаров на остатках.

    Возвращает mapping supply_id -> first_stock_date (или None если не сматчилось).

    ВАЖНО — про честность метода (см. methodology.md §9):
    На складе WB в день приходят товары из множества поставок одновременно,
    а один и тот же nm_id лежит месяцами от прошлых поставок. Поэтому по
    остаткам НЕВОЗМОЖНО точно сказать, какая единица из какой поставки. Любой
    cross-check — приближение.

    Метод (робастный к выбросам):
    Для каждого nm_id из поставки ищем первую дату `quantity > 0` после
    `create_date` (без фильтра «7 дней до», т.к. он отбрасывает реальные
    инкременты от той же поставки — см. nm 11478592 с pre_max=1 → 180).
    Затем берём МЕДИАНУ по всем nm поставки (а не MIN/MAX) — она устойчива
    к nm, которые уже лежали от других поставок (ранний first_seen) и к nm,
    которые ещё не опубликованы (поздний first_seen).

    Медиана ≈ updated_date ± 2 дня — это эмпирическое подтверждение primary-
    метрики с зазором.

    Пакетная реализация: один SQL-запрос на все поставки сразу (не построчно),
    иначе было бы 4000+ запросов к 10.9M-строчной таблице.
    """
    if not supplies:
        return {}
    # Собираем (supply_id, preorder_id, wh, create_d) -> для IN-фильтров.
    # Группируем по складам, т.к. у каждого свои alias'ы.
    by_warehouse: dict[str, list[Supply]] = {}
    for s in supplies:
        by_warehouse.setdefault(s.warehouse_name, []).append(s)

    result: dict[int, date | None] = {}
    for wh, group in by_warehouse.items():
        candidates = aliases.get(wh, [wh])
        # supply_ids этого склада
        sids = [s.supply_id for s in group]
        # Один пакетный SQL: для каждого nm_id в каждой поставке — first_seen
        # (мин. snapshot_date с quantity>0 в окне create_date..+30 дней).
        sql = """
            WITH pick AS (
                SELECT s.supply_id, s.preorder_id,
                       substring(s.create_date from 1 for 10)::date AS create_d,
                       s.warehouse_name AS wh
                FROM supplies s
                WHERE s.supply_id = ANY(%s)
            ),
            nm_per_supply AS (
                SELECT DISTINCT p.supply_id, p.create_d, sg.nm_id
                FROM pick p
                JOIN supply_goods sg
                  ON sg.supply_id = p.supply_id AND sg.preorder_id = p.preorder_id
                WHERE sg.nm_id IS NOT NULL
            ),
            nm_first_seen AS (
                SELECT n.supply_id, n.nm_id,
                       MIN(sd.snapshot_date::date) AS first_seen
                FROM nm_per_supply n
                JOIN stocks_daily_warehouses sd
                  ON sd.nm_id = n.nm_id
                 AND sd.warehouse_name = ANY(%s)
                 AND sd.snapshot_date::date BETWEEN n.create_d AND n.create_d + 30
                 AND sd.quantity > 0
                GROUP BY n.supply_id, n.nm_id
            )
            SELECT
                supply_id,
                PERCENTILE_CONT(0.5) WITHIN GROUP (
                    ORDER BY (first_seen - DATE '2000-01-01')
                ) AS median_off,
                COUNT(*) AS n_with_stock
            FROM nm_first_seen
            WHERE first_seen IS NOT NULL
            GROUP BY supply_id;
        """
        with conn.cursor() as cur:
            cur.execute(sql, (sids, candidates))
            rows = cur.fetchall()
        for r in rows:
            if r["median_off"] is not None:
                result[r["supply_id"]] = date(2000, 1, 1) + _to_timedelta(r["median_off"])
            else:
                result[r["supply_id"]] = None
        # Поставки без результата
        for s in group:
            result.setdefault(s.supply_id, None)
    return result


def _to_timedelta(days: int | float):
    """Преобразует число дней в datetime.timedelta (для сложения с date)."""
    from datetime import timedelta
    return timedelta(days=int(days))


# ──────────────────────────── renderers ────────────────────────────


HEADERS = ["Склад", "Поставок", "Среднее, дн", "P50", "P25", "P75", "P90", "Min", "Max"]


def fmt_float(x: float, digits: int = 1) -> str:
    if x != x:  # NaN
        return "—"
    return f"{x:.{digits}f}"


def build_stats_rows(stats: list[WarehouseStats]) -> list[list]:
    rows = []
    for s in stats:
        rows.append([
            s.warehouse_name,
            s.n_supplies,
            fmt_float(s.mean),
            fmt_float(s.median),
            fmt_float(s.p25),
            fmt_float(s.p75),
            fmt_float(s.p90),
            s.min_lag,
            s.max_lag,
        ])
    return rows


def render_table(stats: list[WarehouseStats], fmt: str) -> str:
    rows = build_stats_rows(stats)
    if fmt == "markdown":
        return tabulate(rows, headers=HEADERS, tablefmt="github")
    if fmt == "csv":
        buf = io.StringIO()
        w = csv.writer(buf)
        w.writerow(HEADERS)
        w.writerows(rows)
        return buf.getvalue().strip()
    # default: human-readable table
    return tabulate(rows, headers=HEADERS, tablefmt="presto")


def render_drilldown(supplies: list[Supply], fmt: str) -> str:
    headers = ["supply_id", "preorder_id", "create_d", "updated_d", "lag, дн", "ready", "accepted"]
    rows = []
    for s in sorted(supplies, key=lambda x: x.create_d):
        rows.append([
            s.supply_id,
            s.preorder_id,
            s.create_d.isoformat(),
            s.updated_d.isoformat(),
            s.lag_days,
            s.ready_for_sale_quantity,
            s.accepted_quantity,
        ])
    if fmt == "markdown":
        return tabulate(rows, headers=headers, tablefmt="github")
    if fmt == "csv":
        buf = io.StringIO()
        w = csv.writer(buf)
        w.writerow(headers)
        w.writerows(rows)
        return buf.getvalue().strip()
    return tabulate(rows, headers=headers, tablefmt="presto")


def render_crosscheck(
    supplies: list[Supply], stock_map: dict[int, date | None], fmt: str
) -> str:
    """Отчёт cross-check: насколько primary-метрика (updated_date) близка к
    эмпирической дате появления товаров на остатках.

    ⚠️ ВАЖНОЕ ОГРАНИЧЕНИЕ МЕТОДА (см. methodology.md §9):
    На складе WB в день приходят товары из множества поставок одновременно,
    а один и тот же nm_id лежит месяцами от прошлых поставок. Поэтому по
    остаткам НЕВОЗМОЖНО точно сказать, какая единица из какой поставки.
    Cross-check — ПРИБЛИЖЕНИЕ, годное только для поставок с большим числом
    уникальных nm (которых не было на складе до поставки). Для типичных
    поставок (где nm уже лежат) cross-check недостоверен.

    Лаг = median_first_seen − updated_date. В среднем по рынку лаг
    положительный (1-2 дня) для поставок с уникальными nm, но может быть
    отрицательным для поставок, где nm уже лежат (старые остатки).
    """
    headers = ["Склад", "Поставок", "Со стоком", "Без стока", "Покрытие, %", "Средний lag (median stock − updated), дн"]
    by_warehouse: dict[str, dict] = {}
    for s in supplies:
        first_stock = stock_map.get(s.supply_id)
        d = by_warehouse.setdefault(s.warehouse_name, {"matched": 0, "total": 0, "lag_sum": 0, "lag_count": 0})
        d["total"] += 1
        if first_stock is not None:
            d["matched"] += 1
            lag = (first_stock - s.updated_d).days
            # Учитываем все лаги (включая отрицательные — это сигнал «nm уже лежали»).
            d["lag_sum"] += lag
            d["lag_count"] += 1
    rows = []
    for wh, d in sorted(by_warehouse.items(), key=lambda kv: -kv[1]["matched"]):
        coverage = (d["matched"] / d["total"] * 100) if d["total"] else 0
        avg_lag = (d["lag_sum"] / d["lag_count"]) if d["lag_count"] else float("nan")
        rows.append([wh, d["total"], d["matched"], d["total"] - d["matched"], fmt_float(coverage), fmt_float(avg_lag)])
    if fmt == "markdown":
        table = tabulate(rows, headers=headers, tablefmt="github")
    elif fmt == "csv":
        buf = io.StringIO()
        w = csv.writer(buf)
        w.writerow(headers)
        w.writerows(rows)
        return buf.getvalue().strip()
    else:
        table = tabulate(rows, headers=headers, tablefmt="presto")
    # Честное предупреждение о пределах метода
    warning = (
        "\n⚠️ Cross-check — ПРИБЛИЖЕНИЕ, а не измерение. На складе WB один nm_id\n"
        "  лежит месяцами от множества поставок, и по остаткам нельзя отличить,\n"
        "  какая единица из какой поставки. Отрицательные лаги = nm уже лежал от\n"
        "  прошлых поставок (старый остаток). Положительные ~1-2 дня = реальные\n"
        "  уникальные nm, появившиеся после поставки. Полный разбор —\n"
        "  references/methodology.md §9."
    )
    return table + warning


# ──────────────────────────── CLI ────────────────────────────


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        prog="supply_time.py",
        description=(
            "Среднее время поставок WB на конкретные склады. "
            "Primary-метрика: updated_date (status_id=5, ready_for_sale>0) − create_date."
        ),
    )
    p.add_argument("--warehouse", type=str, default=None, help="Фильтр по имени склада (точное совпадение).")
    p.add_argument("--since", type=str, default=None, help="Дата с (YYYY-MM-DD), по create_date.")
    p.add_argument("--until", type=str, default=None, help="Дата по (YYYY-MM-DD), по create_date.")
    p.add_argument(
        "--include-in-progress", action="store_true",
        help="Включить поставки в статусах 1-4 (без ready_for_sale>0). Лаг считается по updated_date.",
    )
    # --only-manual по умолчанию True (см. дефолт ниже). --include-auto инвертирует.
    p.add_argument(
        "--include-auto", action="store_true",
        help="Включить автоматические WB-поставки (box_type_id=0: 'Допринято', 'Обезличка', "
             "'QR-приёмка', …). По умолчанию они ОТКЛЮЧЕНЫ, потому что это ночной джоб WB "
             "(например, доприём возвратов из ПВЗ), а не ручное создание менеджером. "
             "Для бизнес-задачи 'время от решения менеджера поставить' нужен только ручной поток.",
    )
    p.add_argument("--drill-down", type=str, default=None,
                   help="Построчный вывод всех поставок на конкретный склад (по имени).")
    p.add_argument("--validate-stocks", action="store_true",
                   help="Cross-check: первая дата stocks.quantity>0 на том же складе (требует aliases).")
    p.add_argument("--format", choices=["table", "markdown", "csv"], default="table",
                   help="Формат вывода (по умолчанию table).")
    p.add_argument("--sort-by", choices=["count", "mean", "median", "p90"], default="count",
                   help="Сортировка отчёта (по умолчанию count — по убыванию количества поставок).")
    p.add_argument("--no-freshness-check", action="store_true",
                   help="Не выводить алерт о свежести данных в начале.")
    p.add_argument("--show-dsn", action="store_true",
                   help="Вывести DSN (с замаскированным паролем) для отладки.")
    return p.parse_args(argv)


def parse_date(s: str) -> date:
    return datetime.strptime(s, "%Y-%m-%d").date()


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        since = parse_date(args.since) if args.since else None
        until = parse_date(args.until) if args.until else None
    except ValueError as e:
        print(f"[error] неверный формат даты (ожидался YYYY-MM-DD): {e}", file=sys.stderr)
        return 2

    try:
        dsn = resolve_dsn()
    except (RuntimeError, FileNotFoundError) as e:
        print(f"[error] {e}", file=sys.stderr)
        return 3
    if args.show_dsn:
        log_dsn(dsn)

    try:
        conn = connect(dsn)
    except Exception as e:
        print(f"[error] подключение к PG не удалось: {e}", file=sys.stderr)
        return 3

    # — freshness alert —
    if not args.no_freshness_check:
        last_date, lag = check_freshness(conn)
        if last_date is None:
            print("[warn] нет завершённых поставок (status_id=5, ready_for_sale>0).", file=sys.stderr)
        else:
            tag = "⚠️" if lag and lag > 3 else "✓"
            print(f"[freshness] {tag} последняя поставка создана {last_date} (lag {lag} дн).",
                  file=sys.stderr)
            if lag and lag > 3:
                print("[freshness] Возможно, устарели данные. Запустите download-wb-supplies-v2.",
                      file=sys.stderr)

    # — mode: drill-down —
    if args.drill_down:
        supplies = fetch_supplies(
            conn, warehouse=args.drill_down, since=since, until=until,
            include_in_progress=args.include_in_progress,
            only_manual=not args.include_auto,
        )
        if not supplies:
            print(f"[info] нет поставок на склад '{args.drill_down}' в заданном диапазоне.",
                  file=sys.stderr)
            return 0
        print(render_drilldown(supplies, args.format))
        return 0

    # — main report —
    supplies = fetch_supplies(
        conn, warehouse=args.warehouse, since=since, until=until,
        include_in_progress=args.include_in_progress,
        only_manual=not args.include_auto,
    )
    if not supplies:
        print("[info] нет поставок по заданным фильтрам.", file=sys.stderr)
        return 0

    if args.validate_stocks:
        aliases = load_aliases()
        stock_map = crosscheck_stocks(conn, supplies, aliases)
        print(render_crosscheck(supplies, stock_map, args.format))
        return 0

    stats = group_by_warehouse(supplies)
    stats = sort_stats(stats, args.sort_by)
    print(render_table(stats, args.format))

    # — краткие текстовые наблюдения (как в sales-trend-report) —
    if stats and args.format != "csv":
        fastest = min(stats, key=lambda s: s.median)
        slowest = max(stats, key=lambda s: s.median)
        print()
        print(f"Самый быстрый склад по медиане: {fastest.warehouse_name} ({fmt_float(fastest.median)} дн).")
        print(f"Самый медленный склад по медиане: {slowest.warehouse_name} ({fmt_float(slowest.median)} дн).")
        big_p90 = next((s for s in stats if s.p90 >= 7 and s.n_supplies >= 3), None)
        if big_p90:
            print(f"⚠️ У «{big_p90.warehouse_name}» P90 = {fmt_float(big_p90.p90)} дн "
                  f"(худший случай для 90% поставок).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
