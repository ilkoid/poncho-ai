// tests/upsert.test.ts — query_id stability (the cross-session join key) + persistDecoded round-trip.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { upsertQuery, upsertQueries } from '../src/db/upsert';
import { persistDecoded } from '../src/db/write';
import { Decode } from '../src/decode';
import { mockIntercepts } from '../src/storage/mock';
import type { Decoded } from '../src/db/types';

beforeEach(async () => {
  // isolate each test: fake-indexeddb keeps the DB in memory across tests in the same file.
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

describe('upsertQuery — query_id stability', () => {
  it('resolves the same text to the same id across calls', async () => {
    const a = await upsertQuery({ query: 'бейсболки для девочки', subject: 'бейсболки', gender: 'для девочки', season: '', age: '', material: '', purpose: '', comment: '', brand: '' });
    const b = await upsertQuery({ query: 'бейсболки для девочки', subject: 'бейсболки', gender: 'для девочки', season: '', age: '', material: '', purpose: '', comment: '', brand: '' });
    expect(a).toBe(b);
    expect(db.search_queries.count()).resolves.toBe(1);
  });

  it('resolves different texts to different ids', async () => {
    const a = await upsertQuery({ query: 'кроссовки', subject: 'кроссовки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' });
    const b = await upsertQuery({ query: 'ботинки', subject: 'ботинки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' });
    expect(a).not.toBe(b);
  });
});

describe('upsertQueries — bulk with dedup', () => {
  it('dedups the same text within a batch and against existing rows', async () => {
    const pre = await upsertQuery({ query: 'кроссовки', subject: 'кроссовки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' });
    const map = await upsertQueries([
      { query: 'кроссовки', subject: 'кроссовки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' }, // exists
      { query: 'кроссовки', subject: 'кроссовки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' }, // dup in batch
      { query: 'ботинки', subject: 'ботинки', gender: '', season: '', age: '', material: '', purpose: '', comment: '', brand: '' }, // new
    ]);
    expect(map.get('кроссовки')).toBe(pre); // stable across the two paths
    expect(map.get('ботинки')).not.toBe(pre);
    expect(map.size).toBe(2);
    expect(db.search_queries.count()).resolves.toBe(2);
  });
});

describe('persistDecoded — round-trip', () => {
  it('writes decoded rows and they are queryable by [query_id+snapshot_ts]', async () => {
    const decoded: Decoded = Decode(mockIntercepts()[0]!, '2026-07-01T10:00:00Z');
    const written = await persistDecoded(decoded);
    expect(written).toBe(2); // two search positions
    const rows = await db.search_positions.where('[query_id+snapshot_ts]').equals([7, '2026-07-01T10:00:00Z']).toArray();
    expect(rows).toHaveLength(2);
  });

  it('full mock bundle persists with the v1 row-count pattern', async () => {
    // one search + one card_detail capture → 2 positions, 1 card, 1 price, 1 detail, 1 stock
    for (const cap of mockIntercepts()) {
      await persistDecoded(Decode(cap, '2026-07-01T10:00:00Z'));
    }
    expect(db.search_positions.count()).resolves.toBe(2);
    expect(db.competitor_cards.count()).resolves.toBe(1);
    expect(db.competitor_card_prices.count()).resolves.toBe(1);
    expect(db.competitor_card_details.count()).resolves.toBe(1);
    expect(db.competitor_card_stocks.count()).resolves.toBe(1);
  });
});
