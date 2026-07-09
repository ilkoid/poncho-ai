// tests/db.smoke.test.ts — S1 DoD: the Dexie DB opens with exactly the 9 stores, and a basic
// round-trip (autoinc PK + read-back) works under fake-indexeddb. Validates the storage layer
// before the decode port lands in S2.

import { describe, it, expect } from 'vitest';
import { db } from '../src/db/dexie';

describe('PonchoDB schema (S1)', () => {
  it('opens with exactly 12 object stores', async () => {
    // any read forces the DB open + version upgrades that create the stores
    await db.search_queries.count();
    const stores = db.tables.map((t) => t.name).sort();
    expect(stores).toEqual(
      [
        'competitor_card_colors',
        'competitor_card_compositions',
        'competitor_card_details',
        'competitor_card_meta',
        'competitor_card_options',
        'competitor_card_prices',
        'competitor_card_sizes',
        'competitor_card_stocks',
        'competitor_cards',
        'search_positions',
        'search_queries',
        'vitrine_ads',
      ].sort(),
    );
  });

  it('round-trips a search_query (incl. the new material/purpose/comment fields)', async () => {
    const id = await db.search_queries.add({
      query: 'бейсболки Nike для девочки летние',
      subject: 'бейсболки',
      brand: 'Nike',
      gender: 'для девочки',
      season: 'летние',
      age: '',
      material: 'текстиль',
      purpose: 'для школы',
      comment: 'демо',
    });
    expect(typeof id).toBe('number');
    const row = await db.search_queries.get(id);
    expect(row?.query).toBe('бейсболки Nike для девочки летние');
    // dimension fields round-trip (stored even though only some are indexed)
    expect(row?.brand).toBe('Nike');
    expect(row?.material).toBe('текстиль');
    expect(row?.purpose).toBe('для школы');
    expect(row?.comment).toBe('демо');
  });

  it('round-trips a search_position with a null query_id (direct nmId target)', async () => {
    // IMPORTANT IndexedDB rule: a key cannot be null/undefined. A record with query_id=null is
    // STORED fine, but it is EXCLUDED from the [query_id+snapshot_ts] compound index (the null
    // member is not a valid key), so querying .equals([null, ts]) throws DataError. Direct nmId
    // targets (null query_id) are therefore fetched via the single-column snapshot_ts index.
    const ts = '2026-07-01T10:00:00Z';
    const id = await db.search_positions.add({
      snapshot_ts: ts,
      query_id: null,
      region_dest: 8038,
      page: 1,
      position: 1,
      nm_id: 111,
      name: 'Test',
      brand: 'Test',
      supplier_id: null,
      panel_promo_id: null,
      price_basic: 100000,
      price_product: 89900,
      rating: 4.5,
      feedbacks: 10,
    });
    const rows = await db.search_positions.where('snapshot_ts').equals(ts).toArray();
    const mine = rows.filter((r) => r.query_id === null);
    expect(mine.length).toBe(1);
    expect(mine[0]?.id).toBe(id);
  });
});
