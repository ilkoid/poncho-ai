// src/reports/visibility.ts — Видимость и динамика. Ranks products in search results for one
// snapshot, and — when a second snapshot is selected — the rank DELTA (improved / deteriorated /
// appeared / disappeared). Focus brands (if any) are highlighted.
//
// Query pattern: by [query_id+snapshot_ts] if a query is selected, else by snapshot_ts. A product
// may appear on several pages/queries in one snapshot; we take its BEST (lowest) position. The
// join is in-memory by nm_id (no full table scan — the index narrows to one snapshot).

import { db } from '../db/dexie';
import { supplierNameMap } from './suppliers';

export interface VisibilityRow {
  nm_id: number;
  name: string; // product title (from search_positions.name)
  brand: string;
  supplier_id: number | null;
  supplier_name: string; // resolved from competitor_cards (search_positions carries only the id)
  is_focus: boolean; // true if brand matches a user-set focus brand (highlight accent only)
  // promo-panel/campaign id if any listing of this nm_id was inside a WB promo panel (null = organic).
  // NOTE: this is PANEL membership, NOT a per-item CPC signal — one panel id can cover most of a
  // query's results (observed: id 1041330 × 167 of 200). A definitive per-item ad flag needs a
  // dedicated recon pass; until then "promo_id != null" means "in a promo panel", not "is an ad".
  // GAP: a per-item CPC ad signal requires cross-referencing panel_promo_id with vitrine_ads (the
  // OРД/erid banners) and the WB /catalog/v2/list promo fields in a dedicated recon pass — tracked
  // separately. This report exposes what we DEFINITELY know: panel membership (here) + distinct
  // banners/advertisers from vitrine_ads (summary.banners / summary.banner_advertisers).
  promo_id: number | null;
  pos_a: number | null;
  pos_b: number | null;
  delta: number | null; // pos_b - pos_a; negative = improved (lower rank is better)
}

export interface VisibilityReport {
  snapshot_a: string;
  snapshot_b: string | null;
  query_id: number | null;
  rows: VisibilityRow[];
  summary: {
    total_a: number;
    total_b: number;
    appeared: number;
    disappeared: number;
    improved: number;
    deteriorated: number;
    promo_panels: number; // distinct promo-panel ids observed in snapshot A
    promo_covered: number; // nm_ids in snapshot A sitting under any promo panel
    banners: number; // distinct erid in vitrine_ads (snapshot A) — real OРД-marked banners
    banner_advertisers: number; // distinct advertiser_inn in vitrine_ads (snapshot A)
  };
}

interface Best {
  pos: number;
  name: string;
  brand: string;
  supplier_id: number | null;
  promo_id: number | null;
}

async function bestPositions(snap: string, queryId: number | null): Promise<Map<number, Best>> {
  const coll =
    queryId != null
      ? db.search_positions.where('[query_id+snapshot_ts]').equals([queryId, snap])
      : db.search_positions.where('snapshot_ts').equals(snap);
  const m = new Map<number, Best>();
  await coll.each((r) => {
    const cur = m.get(r.nm_id);
    if (cur === undefined) {
      m.set(r.nm_id, { pos: r.position, name: r.name, brand: r.brand, supplier_id: r.supplier_id, promo_id: r.panel_promo_id });
    } else {
      if (r.position < cur.pos) {
        cur.pos = r.position;
        cur.name = r.name;
        cur.brand = r.brand;
        cur.supplier_id = r.supplier_id;
      }
      // keep the first promo-panel id seen for this nm_id (a panel repeats across pages)
      if (cur.promo_id == null && r.panel_promo_id != null) cur.promo_id = r.panel_promo_id;
    }
  });
  return m;
}

export async function buildVisibility(
  snapshotA: string,
  snapshotB: string | null,
  queryId: number | null,
  focusBrands: Set<string>,
): Promise<VisibilityReport> {
  const a = await bestPositions(snapshotA, queryId);
  const b = snapshotB ? await bestPositions(snapshotB, queryId) : new Map<number, Best>();
  const suppliers = await supplierNameMap(snapshotB ? [snapshotA, snapshotB] : [snapshotA]);

  const nmids = new Set<number>([...a.keys(), ...b.keys()]);
  const rows: VisibilityRow[] = [];
  let appeared = 0;
  let disappeared = 0;
  let improved = 0;
  let deteriorated = 0;

  for (const nm of nmids) {
    const pa = a.get(nm);
    const pb = b.get(nm);
    const pos_a = pa?.pos ?? null;
    const pos_b = pb?.pos ?? null;
    if (pos_a != null && pos_b != null) {
      const delta = pos_b - pos_a;
      if (delta < 0) improved++;
      else if (delta > 0) deteriorated++;
    } else if (pos_a != null && pos_b == null) {
      disappeared++;
    } else if (pos_a == null && pos_b != null) {
      appeared++;
    }
    const src = pb ?? pa!;
    rows.push({
      nm_id: nm,
      name: src.name,
      brand: src.brand,
      supplier_id: src.supplier_id,
      supplier_name: src.supplier_id != null ? (suppliers.get(src.supplier_id) ?? '') : '',
      is_focus: focusBrands.has(src.brand.toLowerCase()),
      promo_id: pb?.promo_id ?? pa?.promo_id ?? null,
      pos_a,
      pos_b,
      delta: pos_a != null && pos_b != null ? pos_b - pos_a : null,
    });
  }

  // sort by current (B) position, else A — best-ranked first
  rows.sort((x, y) => (x.pos_b ?? x.pos_a ?? Infinity) - (y.pos_b ?? y.pos_a ?? Infinity));

  // promo-panel coverage for snapshot A: how many distinct panels + how many nm_ids they cover.
  // Surfaced in the report so a single broad panel (e.g. 1 id × 167 items) isn't misread as
  // "167 separate ads".
  const aPromo = [...a.values()].filter((b) => b.promo_id != null);
  const promo_panels = new Set(aPromo.map((b) => b.promo_id)).size;
  const promo_covered = aPromo.length;

  // Honest ad signal: distinct OРД banners + advertisers from vitrine_ads (snapshot A). This is the
  // real "how many ads" number — panel_promo_id above is panel MEMBERSHIP, not per-item CPC.
  const adColl =
    queryId != null
      ? db.vitrine_ads.where('[query_id+snapshot_ts]').equals([queryId, snapshotA])
      : db.vitrine_ads.where('snapshot_ts').equals(snapshotA);
  const erids = new Set<string>();
  const advertisers = new Set<string>();
  await adColl.each((a) => {
    if (a.erid) erids.add(a.erid);
    if (a.advertiser_inn) advertisers.add(a.advertiser_inn);
  });

  return {
    snapshot_a: snapshotA,
    snapshot_b: snapshotB,
    query_id: queryId,
    rows,
    summary: {
      total_a: a.size,
      total_b: b.size,
      appeared,
      disappeared,
      improved,
      deteriorated,
      promo_panels,
      promo_covered,
      banners: erids.size,
      banner_advertisers: advertisers.size,
    },
  };
}
