// src/db/write.ts — bulk persistence of decoded fact rows into Dexie.
//
// Two responsibilities:
//   1. chunked bulkAdd (5000 rows/tx) — a single giant add would blow the IDB transaction limits
//      on a 300-query session (~1M price rows);
//   2. null-coerce: any `undefined` field → null. Fact rows are flat, and Dexie does not index
//      undefined members of a compound key ([query_id+snapshot_ts]), which would silently drop
//      rows from index lookups. Decode already emits null, but this is the defensive boundary.

import type { Table } from 'dexie';
import { db } from './dexie';
import type { Decoded } from './types';

const CHUNK = 5000;

/** Coerce top-level undefined → null on a flat row (fact rows have no nested objects). */
function coerceNulls<T extends object>(row: T): T {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(row)) out[k] = v === undefined ? null : v;
  return out as T;
}

/** Chunked bulkAdd into one table. No-op on empty input. */
async function bulkAdd<T extends object>(table: Table<T, number>, rows: readonly T[]): Promise<void> {
  if (rows.length === 0) return;
  for (let i = 0; i < rows.length; i += CHUNK) {
    const slice = rows.slice(i, i + CHUNK).map(coerceNulls);
    await table.bulkAdd(slice);
  }
}

/** Persist all non-empty slices of a Decoded bundle. Tables are written sequentially (predictable
 *  memory + progress); each table's chunks are sequential within. Returns total rows written. */
export async function persistDecoded(d: Decoded): Promise<number> {
  await bulkAdd(db.search_positions, d.search_positions);
  await bulkAdd(db.vitrine_ads, d.vitrine_ads);
  await bulkAdd(db.competitor_cards, d.competitor_cards);
  await bulkAdd(db.competitor_card_prices, d.competitor_card_prices);
  await bulkAdd(db.competitor_card_details, d.competitor_card_details);
  await bulkAdd(db.competitor_card_stocks, d.competitor_card_stocks);
  return (
    d.search_positions.length +
    d.vitrine_ads.length +
    d.competitor_cards.length +
    d.competitor_card_prices.length +
    d.competitor_card_details.length +
    d.competitor_card_stocks.length
  );
}

/** Clear every fact table (keeps search_queries — the dimension is cross-session). Used by
 *  CLEAR_ALL and a fresh mock session. */
export async function clearFacts(): Promise<void> {
  await Promise.all([
    db.search_positions.clear(),
    db.vitrine_ads.clear(),
    db.competitor_cards.clear(),
    db.competitor_card_prices.clear(),
    db.competitor_card_details.clear(),
    db.competitor_card_stocks.clear(),
  ]);
}

// ---- in-session dedup of re-captured fact rows ----
//
// WB's SPA re-fires /list and /detail as the user scrolls/navigates, so the same nm_id (and the
// same per-size/per-wh price+stock rows) is intercepted more than once in one snapshot. bulkAdd is
// append-only, so without dedup these duplicates bloat storage AND skew the competitor-map report
// (ratingCount / priceCount accumulate per row → inflated averages). Within one snapshot the data
// is identical (same nm_id → same feedbacks/rating), so first-occurrence-wins is safe.
//
// The orchestrator owns one SeenKeys per snapshot (reset on COLLECT_LOOP/MOCK_DECODE) and threads
// it through every decoded bundle: cross-intercept duplicates are caught with O(1) Set lookups and
// no DB reads. (vitrine_ads has no stable per-row natural key — promo_id is null — so it is left
// untouched here; ad banners are not aggregated into averages.)

export interface SeenKeys {
  positions: Set<string>; // `${nm_id}|${page}`
  cards: Set<number>; // nm_id
  details: Set<number>; // nm_id
  prices: Set<string>; // `${nm_id}|${size_name}|${wh_id}`
  stocks: Set<string>; // `${nm_id}|${size_name}|${wh_id}`
}

export function freshSeen(): SeenKeys {
  return { positions: new Set(), cards: new Set(), details: new Set(), prices: new Set(), stocks: new Set() };
}

/** Return a new Decoded with already-seen rows dropped (first wins), recording new keys into
 *  `seen`. Pure: no chrome, no DB — unit-testable in isolation. */
export function dedupeBySeen(d: Decoded, seen: SeenKeys): Decoded {
  const szKey = (r: { nm_id: number; size_name: string; wh_id: number | null }): string =>
    `${r.nm_id}|${r.size_name}|${r.wh_id ?? ''}`;
  return {
    search_positions: d.search_positions.filter((r) => {
      // Key includes query_id + region_dest: the same nm on the same page of a DIFFERENT query (or
      // region) is a distinct observation, not a WB re-fire duplicate. The natural key of a position
      // is (query_id, region_dest, page, nm_id, snapshot_ts); omitting query_id used to drop most of
      // Q2..N (popular nms re-rank across many similar queries on the same page) → "only the first
      // query" in reports. Within-query /search re-fires (same key) are still deduped.
      const k = `${r.query_id ?? ''}|${r.region_dest ?? ''}|${r.nm_id}|${r.page}`;
      if (seen.positions.has(k)) return false;
      seen.positions.add(k);
      return true;
    }),
    vitrine_ads: d.vitrine_ads,
    competitor_cards: d.competitor_cards.filter((r) => {
      if (seen.cards.has(r.nm_id)) return false;
      seen.cards.add(r.nm_id);
      return true;
    }),
    competitor_card_details: d.competitor_card_details.filter((r) => {
      if (seen.details.has(r.nm_id)) return false;
      seen.details.add(r.nm_id);
      return true;
    }),
    competitor_card_prices: d.competitor_card_prices.filter((r) => {
      const k = szKey(r);
      if (seen.prices.has(k)) return false;
      seen.prices.add(k);
      return true;
    }),
    competitor_card_stocks: d.competitor_card_stocks.filter((r) => {
      const k = szKey(r);
      if (seen.stocks.has(k)) return false;
      seen.stocks.add(k);
      return true;
    }),
  };
}
