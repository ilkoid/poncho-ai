// tests/price-positioning.test.ts — Ценовое позиционирование. Seeds search_positions directly with
// known prices/feedbacks to verify the index-percentile convention (no interpolation), the p25–p75
// corridor flag, the price↔feedbacks Pearson correlation, and the empty-snapshot edge case.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { buildPricePositioning } from '../src/reports/price-positioning';
import type { SearchPosition } from '../src/db/types';

const SNAP = '2026-07-02T10:00:00Z';

beforeEach(async () => {
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

function position(nm: number, priceKop: number, fb: number): SearchPosition {
  return {
    snapshot_ts: SNAP,
    query_id: 7,
    region_dest: 8038,
    page: 1,
    position: nm,
    nm_id: nm,
    name: `t${nm}`,
    brand: 'X',
    supplier_id: 900,
    panel_promo_id: null,
    price_basic: priceKop,
    price_product: priceKop,
    rating: 4.5,
    feedbacks: fb,
  };
}

describe('price-positioning', () => {
  it('index percentiles (no interpolation), corridor, positive correlation', async () => {
    // prices 10000..100000 step 10000 (n=10); feedbacks 1..10 → perfect positive correlation
    await db.search_positions.bulkAdd([1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((i) => position(i, i * 10000, i)));
    const r = await buildPricePositioning(SNAP, 7);
    expect(r.sample_size).toBe(10);
    // index convention: percentile(p) = sorted[floor((p/100)*(n-1))]
    expect(r.median).toBe(50000); // sorted[4]
    expect(r.p25).toBe(30000); // sorted[2]
    expect(r.p75).toBe(70000); // sorted[6]
    expect(r.summary.min).toBe(10000);
    expect(r.summary.max).toBe(100000);
    // corridor p25..p75 inclusive → 5 nms (30000,40000,50000,60000,70000)
    expect(r.summary.in_corridor).toBe(5);
    const corridorRow = r.rows.find((x) => x.nm_id === 5)!; // price 50000 → in corridor
    expect(corridorRow.in_corridor).toBe(true);
    const below = r.rows.find((x) => x.nm_id === 2)!; // price 20000 < p25
    expect(below.in_corridor).toBe(false);
    // Pearson r ≈ +1 (price = feedbacks × 10000, perfectly linear)
    expect(r.summary.correlation_price_feedbacks).toBeGreaterThan(0.999);
    // 10 equal-width bands, every nm lands in exactly one band
    expect(r.deciles).toHaveLength(10);
    expect(r.deciles.reduce((n, d) => n + d.count, 0)).toBe(10);
  });

  it('negative correlation when feedbacks fall as price rises', async () => {
    await db.search_positions.bulkAdd([position(1, 10000, 30), position(2, 20000, 20), position(3, 30000, 10)]);
    const r = await buildPricePositioning(SNAP, 7);
    expect(r.summary.correlation_price_feedbacks).toBeLessThan(-0.999);
  });

  it('mode picks the band with the most nms', async () => {
    // 4 nms cluster at the low end → band 0 dominates
    await db.search_positions.bulkAdd([
      position(1, 10000, 1), position(2, 11000, 1), position(3, 12000, 1), position(4, 13000, 1),
      position(5, 50000, 1), position(6, 90000, 1),
    ]);
    const r = await buildPricePositioning(SNAP, 7);
    expect(r.sample_size).toBe(6);
    // band 0 (4 nms) is the mode → mode_lo/hi = deciles[0] bounds
    expect(r.deciles[0]!.count).toBe(4);
    expect(r.mode_lo).toBe(r.deciles[0]!.lo);
    expect(r.mode_hi).toBe(r.deciles[0]!.hi);
  });

  it('empty snapshot → null percentiles, no deciles, null correlation', async () => {
    const r = await buildPricePositioning(SNAP, 7);
    expect(r.sample_size).toBe(0);
    expect(r.p25).toBeNull();
    expect(r.median).toBeNull();
    expect(r.p75).toBeNull();
    expect(r.deciles).toEqual([]);
    expect(r.summary.correlation_price_feedbacks).toBeNull();
    expect(r.summary.min).toBeNull();
    expect(r.summary.max).toBeNull();
  });
});
