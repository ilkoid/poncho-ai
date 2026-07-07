// src/export/tables.ts — report model → ExportTable[] converters. Prices are converted kopecks→
// rubles (÷100) so exported files are human-readable; null numerics become empty cells.

import type { ExportTable } from './types';
import type { VisibilityReport } from '../reports/visibility';
import type { CompetitorMapReport } from '../reports/competitor-map';
import type { PricesStocksReport } from '../reports/prices-stocks';
import type { CompetitivenessReport } from '../reports/competitiveness';
import type { PricePositioningReport } from '../reports/price-positioning';

const rub = (kop: number | null): number | null => (kop == null ? null : Math.round(kop) / 100);

export function visibilityToTables(v: VisibilityReport): ExportTable[] {
  return [
    {
      name: 'Видимость',
      columns: ['nm_id', 'name', 'brand', 'supplier_id', 'supplier_name', 'is_focus', 'promo_panel', 'pos_a', 'pos_b', 'delta'],
      rows: v.rows.map((r) => [
        r.nm_id,
        r.name,
        r.brand,
        r.supplier_id ?? '',
        r.supplier_name,
        r.is_focus ? 1 : 0,
        r.promo_id != null ? 1 : 0, // 1 = item sits in a WB promo panel (NOT a per-item CPC signal)
        r.pos_a ?? '',
        r.pos_b ?? '',
        r.delta ?? '',
      ]),
    },
    {
      name: 'Сводка',
      columns: ['metric', 'value'],
      rows: [
        ['total_a', v.summary.total_a],
        ['total_b', v.summary.total_b],
        ['appeared', v.summary.appeared],
        ['disappeared', v.summary.disappeared],
        ['improved', v.summary.improved],
        ['deteriorated', v.summary.deteriorated],
        ['promo_panels', v.summary.promo_panels],
        ['promo_covered', v.summary.promo_covered],
        ['banners (erid)', v.summary.banners],
        ['banner_advertisers (inn)', v.summary.banner_advertisers],
      ],
    },
  ];
}

export function competitivenessToTables(c: CompetitivenessReport): ExportTable[] {
  return [
    {
      name: 'Конкуренты',
      columns: ['supplier_id', 'supplier_name', 'nm_count', 'share', 'is_focus'],
      rows: c.rows.map((r) => [r.supplier_id, r.supplier_name, r.nm_count, Number(r.share.toFixed(4)), r.is_focus ? 1 : 0]),
    },
    {
      name: 'Сводка',
      columns: ['metric', 'value'],
      rows: [
        ['total_suppliers', c.summary.total_suppliers],
        ['total_nms', c.summary.total_nms],
        ['hhi (share of attention, 0..1)', c.summary.hhi],
        ['page1_size', c.summary.page1_size],
        ['page1_promo_covered', c.summary.page1_promo_covered],
        ['page1_promo_coverage_pct', c.summary.page1_promo_coverage_pct],
        ['distinct_banners (erid)', c.summary.distinct_banners],
        ['distinct_advertisers (inn)', c.summary.distinct_advertisers],
        ['total_banner_rows', c.summary.total_banner_rows],
      ],
    },
  ];
}

export function pricePositioningToTables(p: PricePositioningReport): ExportTable[] {
  return [
    {
      name: 'Товары',
      columns: ['nm_id', 'name', 'brand', 'supplier_name', 'price_rub', 'feedbacks', 'rating', 'in_corridor', 'decile'],
      rows: p.rows.map((r) => [r.nm_id, r.name, r.brand, r.supplier_name, rub(r.price), r.feedbacks, Number(r.rating.toFixed(2)), r.in_corridor ? 1 : 0, r.decile]),
    },
    {
      name: 'Децилы цен',
      columns: ['lo_rub', 'hi_rub', 'count', 'median_price_rub', 'median_feedbacks'],
      rows: p.deciles.map((d) => [rub(d.lo), rub(d.hi), d.count, rub(d.median_price), d.median_feedbacks]),
    },
    {
      name: 'Сводка',
      columns: ['metric', 'value'],
      rows: [
        ['sample_size', p.sample_size],
        ['p25_rub', rub(p.p25)],
        ['median_rub', rub(p.median)],
        ['p75_rub', rub(p.p75)],
        ['majority_lo_rub', rub(p.majority_lo)],
        ['majority_hi_rub', rub(p.majority_hi)],
        ['mode_lo_rub', rub(p.mode_lo)],
        ['mode_hi_rub', rub(p.mode_hi)],
        ['in_corridor', p.summary.in_corridor],
        ['min_rub', rub(p.summary.min)],
        ['max_rub', rub(p.summary.max)],
        ['correlation_price_feedbacks', p.summary.correlation_price_feedbacks],
      ],
    },
  ];
}

export function competitorsToTables(m: CompetitorMapReport): ExportTable[] {
  return [
    {
      name: 'Конкуренты',
      columns: ['supplier_id', 'supplier_name', 'nm_count', 'query_count', 'brand_count', 'avg_rating', 'avg_price_rub'],
      rows: m.rows.map((r) => [r.supplier_id, r.supplier_name, r.nm_count, r.query_count, r.brand_count, Number(r.avg_rating.toFixed(2)), rub(r.avg_price)]),
    },
  ];
}

export function pricesToTables(p: PricesStocksReport): ExportTable[] {
  return [
    {
      name: 'Товары',
      columns: ['nm_id', 'name', 'brand', 'supplier', 'price_min_rub', 'price_avg_rub', 'total_qty'],
      rows: p.rows.map((r) => [r.nm_id, r.name, r.brand, r.supplier, rub(r.price_min), rub(r.price_avg), r.total_qty]),
    },
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
