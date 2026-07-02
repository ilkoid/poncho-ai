// src/export/tables.ts — report model → ExportTable[] converters. Prices are converted kopecks→
// rubles (÷100) so exported files are human-readable; null numerics become empty cells.

import type { ExportTable } from './types';
import type { VisibilityReport } from '../reports/visibility';
import type { CompetitorMapReport } from '../reports/competitor-map';
import type { PricesStocksReport } from '../reports/prices-stocks';

const rub = (kop: number | null): number | null => (kop == null ? null : Math.round(kop) / 100);

export function visibilityToTables(v: VisibilityReport): ExportTable[] {
  return [
    {
      name: 'Видимость',
      columns: ['nm_id', 'brand', 'supplier_id', 'is_own', 'promo', 'pos_a', 'pos_b', 'delta'],
      rows: v.rows.map((r) => [
        r.nm_id,
        r.brand,
        r.supplier_id ?? '',
        r.is_own ? 1 : 0,
        r.promo_id != null ? 1 : 0, // 1 = item sits in a WB promo panel (not a per-item CPC signal)
        r.pos_a ?? '',
        r.pos_b ?? '',
        r.delta ?? '',
      ]),
    },
  ];
}

export function competitorsToTables(m: CompetitorMapReport): ExportTable[] {
  return [
    {
      name: 'Конкуренты',
      columns: ['supplier_id', 'supplier_name', 'nm_count', 'query_count', 'avg_rating', 'avg_price_rub'],
      rows: m.rows.map((r) => [r.supplier_id, r.supplier_name, r.nm_count, r.query_count, Number(r.avg_rating.toFixed(2)), rub(r.avg_price)]),
    },
  ];
}

export function pricesToTables(p: PricesStocksReport): ExportTable[] {
  return [
    {
      name: 'Гистограмма цен',
      columns: ['bucket_lo_rub', 'bucket_hi_rub', 'count'],
      rows: p.histogram.map((b) => [rub(b.lo), rub(b.hi), b.count]),
    },
    {
      name: 'Out of stock',
      columns: ['nm_id', 'brand', 'total_qty'],
      rows: p.out_of_stock.map((o) => [o.nm_id, o.brand, o.total_qty]),
    },
  ];
}
