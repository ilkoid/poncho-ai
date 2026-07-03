// src/reports/snapshots.ts — the snapshot picker source. Lists distinct snapshot_ts values across
// the fact tables (newest first), so the reports tab can offer "session A [vs session B]".
//
// Uses Dexie's orderBy('snapshot_ts').uniqueKeys() — an indexed distinct scan, not a table dump.
// We union three tables (positions/cards/ads) because a snapshot may have only some of them
// (e.g. a mock session has positions + cards but maybe no ads).

import { db } from '../db/dexie';

/** All distinct snapshot timestamps present in the DB, newest first. */
export async function listSnapshots(): Promise<string[]> {
  const [pos, cards, ads] = await Promise.all([
    db.search_positions.orderBy('snapshot_ts').uniqueKeys(),
    db.competitor_cards.orderBy('snapshot_ts').uniqueKeys(),
    db.vitrine_ads.orderBy('snapshot_ts').uniqueKeys(),
  ]).catch(() => [[], [], []] as string[][]);
  const all = new Set<string>();
  for (const k of [...pos, ...cards, ...ads]) {
    if (typeof k === 'string') all.add(k);
  }
  return [...all].sort().reverse(); // newest first (ISO-8601 sorts lexicographically = chronologically)
}

/** All distinct query texts for a snapshot (for the visibility report's query filter). */
export async function listQueriesForSnapshot(snapshot: string): Promise<{ query_id: number; query: string }[]> {
  const qids = new Set<number>();
  await db.search_positions
    .where('snapshot_ts')
    .equals(snapshot)
    .each((r) => {
      if (r.query_id != null) qids.add(r.query_id);
    });
  if (qids.size === 0) return [];
  const rows = await db.search_queries.bulkGet([...qids]);
  return rows
    .filter((r): r is NonNullable<typeof r> => r != null)
    .map((r) => ({ query_id: r.query_id!, query: r.query }));
}
