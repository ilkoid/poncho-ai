// src/reports/competitor-map.ts — Конкуренты по ассортименту. Aggregates SEARCH POSITIONS by
// supplier for one snapshot: how many distinct products (nm_count) and queries (query_count) each
// competitor covers, their average rating, average listing price, and brand count. A supplier
// carrying any focus brand is highlighted.
//
// Source = search_positions (the authoritative ranking), NOT competitor_cards. competitor_cards
// only covers the ~detailK cards the harvest deep-opened (plus, historically, carousel noise), so
// aggregating it badly under-counts competitors and used to leak cross-category recommendations.
// Positions answer "who actually ranked for this query" — the question this report asks.
//
// supplier_name is absent from positions (only the numeric id), so it is resolved from
// competitor_cards via the shared supplierNameMap (one indexed scan per snapshot).

import { db } from '../db/dexie';
import { supplierNameMap } from './suppliers';

export interface CompetitorRow {
  supplier_id: number;
  supplier_name: string;
  nm_count: number;
  query_count: number;
  brand_count: number; // distinct brands this supplier carries
  avg_rating: number;
  avg_price: number | null; // kopecks; mean of per-nm listing price_product
  is_focus: boolean; // true if any of this supplier's ranked products carries a focus brand
}

export interface CompetitorMapReport {
  snapshot: string;
  query_id: number | null;
  rows: CompetitorRow[];
}

interface SupAgg {
  nms: Set<number>;
  queries: Set<number>;
  brands: Set<string>; // distinct brand names this supplier carries
  prices: Map<number, number>; // nm_id → listing price_product (constant per nm; dedup multi-page)
  ratingSum: number;
  ratingCount: number;
  hasFocus: boolean; // set when any ranked product's brand matches a focus brand
}

export async function buildCompetitorMap(
  snapshot: string,
  queryId: number | null,
  focusBrands: Set<string>,
): Promise<CompetitorMapReport> {
  const posColl =
    queryId != null
      ? db.search_positions.where('[query_id+snapshot_ts]').equals([queryId, snapshot])
      : db.search_positions.where('snapshot_ts').equals(snapshot);
  const sup = new Map<number, SupAgg>();
  await posColl.each((r) => {
    if (r.supplier_id == null) return;
    let s = sup.get(r.supplier_id);
    if (!s) {
      s = { nms: new Set(), queries: new Set(), brands: new Set(), prices: new Map(), ratingSum: 0, ratingCount: 0, hasFocus: false };
      sup.set(r.supplier_id, s);
    }
    s.nms.add(r.nm_id);
    s.brands.add(r.brand);
    s.prices.set(r.nm_id, r.price_product); // identical across pages; last write is the same value
    if (r.query_id != null) s.queries.add(r.query_id);
    s.ratingSum += r.rating;
    s.ratingCount++;
    if (focusBrands.has(r.brand.toLowerCase())) s.hasFocus = true; // supplier carries a focus brand
  });

  const suppliers = await supplierNameMap([snapshot]);

  const rows: CompetitorRow[] = [];
  for (const [sid, s] of sup) {
    let pSum = 0;
    for (const p of s.prices.values()) pSum += p;
    rows.push({
      supplier_id: sid,
      supplier_name: suppliers.get(sid) ?? '',
      nm_count: s.nms.size,
      query_count: s.queries.size,
      brand_count: s.brands.size,
      avg_rating: s.ratingCount ? s.ratingSum / s.ratingCount : 0,
      avg_price: s.prices.size ? Math.round(pSum / s.prices.size) : null,
      is_focus: s.hasFocus,
    });
  }
  rows.sort((a, b) => b.nm_count - a.nm_count); // competitors with the most products first
  return { snapshot, query_id: queryId, rows };
}
