// tests/collect.test.ts — simulates the offscreen MOCK_DECODE path (the no-browser test hook):
// decode each mock intercept → persistDecoded → accumulate counts. This is the exact logic
// orchestrator.runMockDecode() runs minus chrome.runtime, so it proves the WRITE PIPELINE is
// independent of the service worker (the SW never imports Decode/persistDecoded — only the
// offscreen does). Killing the SW mid-session cannot lose data because writes live here.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { Decode } from '../src/decode';
import { persistDecoded } from '../src/db/write';
import { mockIntercepts } from '../src/storage/mock';
import type { FactCounts } from '../src/messages';
import { EMPTY_COUNTS } from '../src/messages';

beforeEach(async () => {
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

describe('offscreen mock-session path (SW-independent writes)', () => {
  it('decodes + persists mock intercepts, accumulating counts that match the DB', async () => {
    const snap = '2026-07-02T00:00:00Z';
    const counts: FactCounts = { ...EMPTY_COUNTS };

    // mirror orchestrator.runMockDecode: per-intercept decode → persist → count
    for (const it of mockIntercepts()) {
      const decoded = Decode(it, snap);
      for (const k of Object.keys(counts) as (keyof FactCounts)[]) {
        counts[k] += decoded[k].length;
      }
      await persistDecoded(decoded);
    }

    // accumulated counts must equal what actually landed in Dexie
    expect(counts.search_positions).toBe(2);
    expect(counts.competitor_cards).toBe(1);
    expect(counts.competitor_card_prices).toBe(1);
    expect(counts.competitor_card_details).toBe(1);
    expect(counts.competitor_card_stocks).toBe(1);
    expect(counts.search_positions).toBe(await db.search_positions.count());
    expect(counts.competitor_cards).toBe(await db.competitor_cards.count());
    expect(counts.competitor_card_stocks).toBe(await db.competitor_card_stocks.count());

    // provenance: every row carries the intercept's query_id (7) + the session snapshot
    const pos = await db.search_positions.where('[query_id+snapshot_ts]').equals([7, snap]).toArray();
    expect(pos).toHaveLength(2);
    expect(pos.every((r) => r.query_id === 7)).toBe(true);
  });
});
