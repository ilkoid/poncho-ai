// tests/reports.test.ts — the three report families on mock data. Seeds snapshot A from
// mockIntercepts (search + card_detail) and a second snapshot B with a shifted search result, then
// checks visibility (rank + delta), competitor map (supplier aggregation), and prices/stocks.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { Decode } from '../src/decode';
import { persistDecoded } from '../src/db/write';
import { mockIntercepts } from '../src/storage/mock';
import { upsertQuery } from '../src/db/upsert';
import { buildVisibility } from '../src/reports/visibility';
import { buildCompetitorMap } from '../src/reports/competitor-map';
import { buildPricesStocks } from '../src/reports/prices-stocks';
import { listSnapshots } from '../src/reports/snapshots';
import type { Intercept } from '../src/db/types';

const SNAP_A = '2026-07-02T10:00:00Z';
const SNAP_B = '2026-07-02T12:00:00Z';

beforeEach(async () => {
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

async function seedMock(snapshot: string): Promise<void> {
  for (const it of mockIntercepts()) {
    await persistDecoded(Decode(it, snapshot));
  }
}

describe('snapshots', () => {
  it('lists distinct snapshot_ts newest-first', async () => {
    await seedMock(SNAP_A);
    await seedMock(SNAP_B);
    const all = await listSnapshots();
    expect(all[0]).toBe(SNAP_B); // newest first
    expect(all).toContain(SNAP_A);
  });
});

describe('visibility', () => {
  it('single snapshot: best rank per nm_id, focus brand highlighted', async () => {
    await seedMock(SNAP_A); // search: nm111(Nike)@101, nm222(Adidas)@102 (query 7); card: nm111 supplier 900
    const r = await buildVisibility(SNAP_A, null, 7, new Set(['nike']));
    expect(r.summary.total_a).toBe(2);
    expect(r.rows[0]!.nm_id).toBe(111); // pos 101 < 102 → first
    expect(r.rows[0]!.pos_a).toBe(101);
    expect(r.rows[0]!.is_focus).toBe(true); // brand Nike matches the focus set (case-insensitive)
    expect(r.rows[1]!.nm_id).toBe(222);
    expect(r.rows[1]!.is_focus).toBe(false); // Adidas ≠ focus
    expect(r.snapshot_b).toBeNull();
    // promo-panel coverage: mock search has nm111 (organic) + nm222 (panelPromoId 99)
    expect(r.summary.promo_panels).toBe(1); // one distinct panel id (99)
    expect(r.summary.promo_covered).toBe(1); // only nm222 sits under it
    expect(r.rows.find((x) => x.nm_id === 222)!.promo_id).toBe(99);
    expect(r.rows.find((x) => x.nm_id === 111)!.promo_id).toBeNull();
    // supplier name resolved via the competitor_cards join (search_positions carries only the id)
    expect(r.rows.find((x) => x.nm_id === 111)!.supplier_name).toBe('ООО Рога');
    expect(r.rows.find((x) => x.nm_id === 222)!.supplier_name).toBe(''); // no card for supplier 901
  });

  it('two snapshots: delta (improved / disappeared)', async () => {
    await seedMock(SNAP_A); // nm111@101, nm222@102
    // SNAP_B: nm111 now at position 1 (page 1, index 0 → improved from 101), nm222 gone (disappeared)
    const searchB: Intercept = {
      kind: 'search',
      url: 'https://w.ru/s?search=x&page=1&dest=8038',
      query_id: 7,
      status: 200,
      body: { products: [{ id: 111, name: 'Кроссовки Nike', brand: 'Nike', supplierId: 900, sizes: [{ price: { basic: 100000, product: 89900 } }] }] },
    };
    await persistDecoded(Decode(searchB, SNAP_B));
    const r = await buildVisibility(SNAP_A, SNAP_B, 7, new Set(['nike']));
    const nm111 = r.rows.find((x) => x.nm_id === 111)!;
    expect(nm111.pos_a).toBe(101);
    expect(nm111.pos_b).toBe(1);
    expect(nm111.delta).toBe(-100); // 1 - 101, improved (lower rank)
    expect(nm111.name).toBe('Кроссовки Nike'); // product title flows through search_positions.name
    expect(r.summary.improved).toBe(1);
    expect(r.summary.disappeared).toBe(1); // nm222 in A, absent in B
  });
});

describe('competitor map', () => {
  it('aggregates RANKED POSITIONS by supplier (not cards): nm_count + brands + avg listing price', async () => {
    await seedMock(SNAP_A); // search qid=7: nm111 (Nike, sup 900, 89900, 4.5) + nm222 (Adidas, sup 901, 45000, 4.0)
    const r = await buildCompetitorMap(SNAP_A, 7, new Set(['nike']));
    expect(r.rows).toHaveLength(2); // two distinct suppliers ranked (built from search_positions, not cards)
    const s900 = r.rows.find((x) => x.supplier_id === 900)!;
    const s901 = r.rows.find((x) => x.supplier_id === 901)!;
    expect(s900.supplier_name).toBe('ООО Рога'); // resolved from competitor_cards
    expect(s900.nm_count).toBe(1);
    expect(s900.query_count).toBe(1);
    expect(s900.brand_count).toBe(1); // Nike
    expect(s900.avg_price).toBe(89900); // listing price_product from positions
    expect(s900.avg_rating).toBeCloseTo(4.5);
    expect(s900.is_focus).toBe(true); // carries Nike (focus brand)
    expect(s901.supplier_name).toBe(''); // no card_detail for nm222 → name unknown (id-only)
    expect(s901.brand_count).toBe(1); // Adidas
    expect(s901.avg_price).toBe(45000);
    expect(s901.is_focus).toBe(false); // Adidas ≠ focus
  });
});

describe('prices & stocks', () => {
  it('histogram + OOP detection', async () => {
    await seedMock(SNAP_A); // nm111: price 89900, stock qty 10 (wh 507) → in stock
    const r = await buildPricesStocks(SNAP_A, 7);
    expect(r.price_count).toBe(1);
    expect(r.histogram.reduce((n, b) => n + b.count, 0)).toBe(1);
    expect(r.in_stock_count).toBe(1);
    expect(r.out_of_stock).toHaveLength(0);
    // per-product table: nm111 from the card_detail capture
    const row111 = r.rows.find((x) => x.nm_id === 111)!;
    expect(row111).toBeDefined();
    expect(row111.supplier).toBe('ООО Рога');
    expect(row111.brand).toBe('Nike');
    expect(row111.price_min).toBe(89900);
    expect(row111.price_avg).toBe(89900);
    expect(row111.total_qty).toBe(10); // summed warehouse stock (wh 507 × qty 10)
  });

  it('flags qty=0 as out of print', async () => {
    await seedMock(SNAP_A);
    // add a second card with zero stock
    const zeroDetail: Intercept = {
      kind: 'card_detail',
      url: 'https://w.ru/d',
      query_id: 7,
      status: 200,
      body: { products: [{ id: 999, brand: 'Empty', sizes: [{ name: '1', price: { basic: 5000, product: 5000 }, stocks: [{ wh: 1, qty: 0 }] }] }] },
    };
    await persistDecoded(Decode(zeroDetail, SNAP_A));
    const r = await buildPricesStocks(SNAP_A, 7);
    expect(r.out_of_stock.find((o) => o.nm_id === 999)).toBeDefined();
    expect(r.rows.find((x) => x.nm_id === 999)).toBeDefined(); // zero-stock card still gets a table row
  });
});

// smoke: upsertQuery still works inside a report test (query text round-trip)
describe('queries', () => {
  it('upsertQuery assigns a stable id', async () => {
    const id = await upsertQuery({ query: 'test', subject: '', brand: '', gender: '', season: '', age: '', material: '', purpose: '', comment: '' });
    expect(typeof id).toBe('number');
  });
});
