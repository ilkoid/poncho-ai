// src/export/json-dump.ts — dump the FULL raw captured data for one snapshot as JSON (all 6 fact
// tables + the search_queries referenced). This is the "show me the real WB result" export — the
// complete per-session dataset, not a report summary. Pretty-printed (2-space) for human/AI reading.
//
// Scoped to one snapshot to bound size (a 300-query session is ~940 MB in IDB → a whole-DB dump
// would OOM the tab). The snapshot_ts index makes each table read an indexed lookup, not a scan.

import { db } from '../db/dexie';

export interface SnapshotDump {
  generated_at: string;
  snapshot: string;
  counts: Record<string, number>;
  search_queries: unknown[];
  search_positions: unknown[];
  vitrine_ads: unknown[];
  competitor_cards: unknown[];
  competitor_card_prices: unknown[];
  competitor_card_details: unknown[];
  competitor_card_stocks: unknown[];
  competitor_card_meta: unknown[];
  competitor_card_options: unknown[];
  competitor_card_compositions: unknown[];
  competitor_card_sizes: unknown[];
  competitor_card_colors: unknown[];
}

/** Read every captured row for one snapshot (all 11 fact tables) + the queries referenced in it. */
export async function dumpSnapshot(snapshot: string): Promise<SnapshotDump> {
  const [sp, va, cc, cp, cd, cs, cm, co, ccn, csz, ccl] = await Promise.all([
    db.search_positions.where('snapshot_ts').equals(snapshot).toArray(),
    db.vitrine_ads.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_cards.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_prices.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_details.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_stocks.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_meta.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_options.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_compositions.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_sizes.where('snapshot_ts').equals(snapshot).toArray(),
    db.competitor_card_colors.where('snapshot_ts').equals(snapshot).toArray(),
  ]);

  // resolve the query texts for every query_id that appears in this snapshot's rows
  const qids = new Set<number>();
  for (const r of sp) if (r.query_id != null) qids.add(r.query_id);
  for (const r of cc) if (r.query_id != null) qids.add(r.query_id);
  let queries: unknown[] = [];
  if (qids.size > 0) {
    const qrows = await db.search_queries.bulkGet([...qids]);
    queries = qrows.filter((q): q is NonNullable<typeof q> => q != null);
  }

  return {
    generated_at: new Date().toISOString(),
    snapshot,
    counts: {
      search_queries: queries.length,
      search_positions: sp.length,
      vitrine_ads: va.length,
      competitor_cards: cc.length,
      competitor_card_prices: cp.length,
      competitor_card_details: cd.length,
      competitor_card_stocks: cs.length,
      competitor_card_meta: cm.length,
      competitor_card_options: co.length,
      competitor_card_compositions: ccn.length,
      competitor_card_sizes: csz.length,
      competitor_card_colors: ccl.length,
    },
    search_queries: queries,
    search_positions: sp,
    vitrine_ads: va,
    competitor_cards: cc,
    competitor_card_prices: cp,
    competitor_card_details: cd,
    competitor_card_stocks: cs,
    competitor_card_meta: cm,
    competitor_card_options: co,
    competitor_card_compositions: ccn,
    competitor_card_sizes: csz,
    competitor_card_colors: ccl,
  };
}

/** Serialize + trigger a browser download of `obj` as pretty-printed JSON. */
export function downloadJSON(filenameBase: string, obj: unknown): void {
  const text = JSON.stringify(obj, null, 2);
  const blob = new Blob([text], { type: 'application/json;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  void chrome.downloads
    .download({ url, filename: `${filenameBase}.json`, saveAs: false })
    .then(() => URL.revokeObjectURL(url))
    .catch(() => URL.revokeObjectURL(url));
}
