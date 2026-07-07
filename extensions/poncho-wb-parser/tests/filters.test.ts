// tests/filters.test.ts — pure unit tests for the structured report filter (applyFilter). No Dexie
// needed; applyFilter is a pure function over rows + a fields extractor. Mirrors the layering in the
// dashboard: structured filter slices rows BEFORE the per-table substring search runs.

import { describe, it, expect } from 'vitest';
import { applyFilter, isEmptyFilter, DEFAULT_REPORT_FILTER, type ReportFilter, type FilterableRow } from '../src/reports/filters';

interface Row {
  nm: number;
  price_kop: number;
  brand: string;
  rating: number;
  fb: number;
  sid: number;
}

const ROWS: Row[] = [
  { nm: 1, price_kop: 50000, brand: 'Nike Air', rating: 4.8, fb: 100, sid: 900 },
  { nm: 2, price_kop: 150000, brand: 'Adidas', rating: 4.2, fb: 30, sid: 901 },
  { nm: 3, price_kop: 90000, brand: 'Nike Run', rating: 3.9, fb: 5, sid: 900 },
  { nm: 4, price_kop: 200000, brand: 'Puma', rating: 4.9, fb: 500, sid: 902 },
];

const fields = (r: Row): FilterableRow => ({ price_kop: r.price_kop, brand: r.brand, rating: r.rating, feedbacks: r.fb, supplier_id: r.sid });

describe('isEmptyFilter', () => {
  it('true for the default filter', () => {
    expect(isEmptyFilter(DEFAULT_REPORT_FILTER)).toBe(true);
  });
  it('false as soon as any criterion is set', () => {
    expect(isEmptyFilter({ ...DEFAULT_REPORT_FILTER, rating_min: 4.0 })).toBe(false);
    expect(isEmptyFilter({ ...DEFAULT_REPORT_FILTER, brands_exclude: ['puma'] })).toBe(false);
  });
});

describe('applyFilter', () => {
  it('empty filter = passthrough (same array, no copy)', () => {
    const out = applyFilter(ROWS, DEFAULT_REPORT_FILTER, fields);
    expect(out).toBe(ROWS); // identical reference — fast path
    expect(out).toHaveLength(4);
  });

  it('price band is inclusive and converts rubles → kopecks', () => {
    // 600–1000 ₽ band → 60000–100000 kop → keeps only row 3 (90000)
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, price_min_rub: 600, price_max_rub: 1000 };
    const out = applyFilter(ROWS, f, fields);
    expect(out.map((r) => r.nm)).toEqual([3]);
  });

  it('brands_include: case-insensitive substring, OR semantics', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, brands_include: ['NIKE'] };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm)).toEqual([1, 3]); // 'Nike Air' and 'Nike Run'
  });

  it('brands_exclude: drops any brand containing a term', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, brands_exclude: ['nike'] };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm)).toEqual([2, 4]);
  });

  it('rating_min is inclusive', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, rating_min: 4.2 };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm).sort()).toEqual([1, 2, 4]); // 4.8, 4.2, 4.9
  });

  it('feedbacks_min is inclusive', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, feedbacks_min: 100 };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm).sort()).toEqual([1, 4]); // fb 100, 500
  });

  it('suppliers_include: whitelist by supplier_id', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, suppliers_include: [900, 902] };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm).sort()).toEqual([1, 3, 4]);
  });

  it('suppliers_exclude: blacklist by supplier_id', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, suppliers_exclude: [900] };
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm)).toEqual([2, 4]);
  });

  it('row missing the field an active criterion needs is EXCLUDED', () => {
    // rows without a price must drop when a price band is set
    const sparse: Row[] = [{ nm: 9, price_kop: 0, brand: '', rating: 0, fb: 0, sid: 0 }];
    const fieldsSparse = (r: Row): FilterableRow => ({ brand: r.brand, supplier_id: r.sid }); // no price_kop exposed
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, price_min_rub: 100 };
    expect(applyFilter(sparse, f, fieldsSparse)).toHaveLength(0);
  });

  it('multiple criteria compose (AND)', () => {
    const f: ReportFilter = { ...DEFAULT_REPORT_FILTER, brands_include: ['nike'], feedbacks_min: 50 };
    // Nike rows: 1 (fb100), 3 (fb5). With feedbacks>=50 → only 1.
    expect(applyFilter(ROWS, f, fields).map((r) => r.nm)).toEqual([1]);
  });
});
