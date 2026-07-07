// tests/export.test.ts — CSV serialization (BOM + RFC-4180 escaping) + report→table converters.
// No browser needed: toCSV is pure; the converters are pure over the report models.

import { describe, it, expect } from 'vitest';
import { toCSV } from '../src/export/csv';
import { visibilityToTables, competitorsToTables, pricesToTables } from '../src/export/tables';
import type { VisibilityReport } from '../src/reports/visibility';
import type { CompetitorMapReport } from '../src/reports/competitor-map';
import type { PricesStocksReport } from '../src/reports/prices-stocks';

describe('toCSV', () => {
  it('prepends a UTF-8 BOM (Excel decodes Cyrillic)', () => {
    const csv = toCSV({ name: 't', columns: ['a'], rows: [['й']] });
    expect(csv.charCodeAt(0)).toBe(0xfeff); // BOM
    expect(csv.endsWith('й')).toBe(true);
  });

  it('quotes cells with comma / quote / newline and doubles inner quotes', () => {
    const csv = toCSV({ name: 't', columns: ['c'], rows: [['x, y'], ['he said "hi"'], ['line1\nline2']] });
    expect(csv).toContain('"x, y"');
    expect(csv).toContain('"he said ""hi"""');
    expect(csv).toContain('"line1\nline2"');
  });
});

const vis: VisibilityReport = {
  snapshot_a: 'A', snapshot_b: null, query_id: 1,
  rows: [{ nm_id: 111, name: 'Кроссовки Nike', brand: 'Nike', supplier_id: 900, supplier_name: 'ООО Рога', is_focus: true, promo_id: null, pos_a: 5, pos_b: null, delta: null }],
  summary: { total_a: 1, total_b: 0, appeared: 0, disappeared: 0, improved: 0, deteriorated: 0, promo_panels: 0, promo_covered: 0, banners: 0, banner_advertisers: 0 },
};

describe('report → table converters', () => {
  it('visibility → Видимость (promo_panel column) + Сводка (banners in summary)', () => {
    const t = visibilityToTables(vis);
    expect(t[0]!.name).toBe('Видимость');
    expect(t[0]!.columns).toContain('nm_id');
    expect(t[0]!.columns).toContain('supplier_name');
    expect(t[0]!.columns).toContain('promo_panel'); // renamed from 'promo' (panel, not per-item ad)
    expect(t[0]!.columns).not.toContain('promo');
    expect(t[0]!.rows[0]).toContain(111);
    expect(t[0]!.rows[0]).toContain('ООО Рога');
    // second sheet = summary, surfaces the honest banners signal
    expect(t[1]!.name).toBe('Сводка');
    const metrics = t[1]!.rows.map((r) => r[0]);
    expect(metrics).toContain('banners (erid)');
    expect(metrics).toContain('banner_advertisers (inn)');
  });

  it('competitors → table with brand_count + avg_price converted to rubles', () => {
    const m: CompetitorMapReport = {
      snapshot: 'A', query_id: null,
      rows: [{ supplier_id: 900, supplier_name: 'ООО Рога', nm_count: 3, query_count: 2, brand_count: 2, avg_rating: 4.5, avg_price: 89900, is_focus: true }],
    };
    const t = competitorsToTables(m);
    expect(t[0]!.columns).toContain('brand_count');
    // avg_price_rub is the last column; 89900 kop → 899.00 rub
    const lastIdx = t[0]!.columns.length - 1;
    expect(t[0]!.rows[0]![lastIdx]).toBe(899);
  });

  it('prices → товары (first, for CSV) + histogram + OOP tables', () => {
    const p: PricesStocksReport = {
      snapshot: 'A', query_id: null,
      histogram: [{ lo: 100000, hi: 110000, count: 5 }],
      price_count: 5,
      out_of_stock: [{ nm_id: 999, brand: 'Empty', total_qty: 0 }],
      in_stock_count: 4,
      rows: [{ nm_id: 111, name: 'Кроссовки', brand: 'Nike', supplier: 'ООО Рога', price_min: 89900, price_avg: 89900, total_qty: 10 }],
    };
    const t = pricesToTables(p);
    expect(t).toHaveLength(3);
    expect(t[0]!.name).toBe('Товары'); // first → CSV (tables[0]) ships the useful per-product table
    expect(t[1]!.name).toBe('Гистограмма цен');
    expect(t[2]!.name).toBe('Out of stock');
    expect(t[1]!.rows[0]![0]).toBe(1000); // lo 100000 kop → 1000.00 rub
    expect(t[0]!.rows[0]).toContain('ООО Рога');
  });
});
