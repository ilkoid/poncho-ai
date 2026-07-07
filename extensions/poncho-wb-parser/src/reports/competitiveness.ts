// src/reports/competitiveness.ts — Конкурентность ниши. Answers "how saturated is this query /
// niche — how many real competitors, how concentrated (HHI), how aggressively are promo panels and
// banners plastered across page 1?". A high HHI + heavy promo coverage → an expensive, crowded
// niche; low HHI + organic page 1 → a soft target.
//
// Source = search_positions (the FULL ranked snapshot, ~100/page — not the top-K detail cards) joined
// with vitrine_ads for the honest "banners" signal. One indexed scan per table, scoped by
// [query_id+snapshot_ts] when a query is picked, else by snapshot_ts.
//
// Two advertising signals, deliberately separated (see reports/visibility.ts:19-23 for the same
// reasoning): `panel_promo_id != null` on a position = WB promo-PANEL membership (one panel can
// cover most of a query's results — observed 167/200), NOT a per-item CPC ad. `vitrine_ads`
// (banners with OРД erid) = real, legally-marked advertisements. Surfacing both, labeled, is the
// honest version of "how much advertising is in this niche" until a dedicated recon pass can attribute
// per-item CPC.
//
// HHI is computed over distinct nm_count per supplier ("share of attention"), NOT revenue — WB exposes
// no sales. Niche with one supplier holding all nm_ids → HHI = 1.0 (monopoly); perfectly even split
// across S suppliers → 1/S. The UI label says "доля внимания, не выручки".

import { db } from '../db/dexie';
import { supplierNameMap } from './suppliers';

export interface CompetitivenessRow {
  supplier_id: number;
  supplier_name: string;
  nm_count: number; // distinct nm_ids this supplier has ranked in the snapshot
  share: number; // nm_count / total_nms, 0..1
  is_focus: boolean; // any of this supplier's ranked products carries a focus brand
}

export interface CompetitivenessSummary {
  total_suppliers: number; // distinct supplier_id
  total_nms: number; // distinct nm_id
  hhi: number; // Σ (n_s / N)², 0..1 — "share of attention"
  page1_size: number; // search_positions rows on page 1 (scoped)
  page1_promo_covered: number; // of those, rows with panel_promo_id != null
  page1_promo_coverage_pct: number; // 0..100
  distinct_banners: number; // distinct erid in vitrine_ads (scoped)
  distinct_advertisers: number; // distinct advertiser_inn
  total_banner_rows: number; // raw vitrine_ads row count (a banner may repeat across pages)
}

export interface CompetitivenessReport {
  snapshot: string;
  query_id: number | null;
  rows: CompetitivenessRow[]; // one per supplier, most nm_count first
  summary: CompetitivenessSummary;
}

interface SupAgg {
  nms: Set<number>;
  hasFocus: boolean;
}

export async function buildCompetitiveness(
  snapshot: string,
  queryId: number | null,
  focusBrands: Set<string>,
): Promise<CompetitivenessReport> {
  const posColl =
    queryId != null
      ? db.search_positions.where('[query_id+snapshot_ts]').equals([queryId, snapshot])
      : db.search_positions.where('snapshot_ts').equals(snapshot);
  const adColl =
    queryId != null
      ? db.vitrine_ads.where('[query_id+snapshot_ts]').equals([queryId, snapshot])
      : db.vitrine_ads.where('snapshot_ts').equals(snapshot);

  const sup = new Map<number, SupAgg>();
  const allNms = new Set<number>();
  let page1Size = 0;
  let page1Promo = 0;
  await posColl.each((r) => {
    allNms.add(r.nm_id);
    if (r.page === 1) {
      page1Size++;
      if (r.panel_promo_id != null) page1Promo++;
    }
    if (r.supplier_id == null) return; // unknown supplier → counts toward total_nms but no row
    let s = sup.get(r.supplier_id);
    if (!s) {
      s = { nms: new Set(), hasFocus: false };
      sup.set(r.supplier_id, s);
    }
    s.nms.add(r.nm_id);
    if (focusBrands.has(r.brand.toLowerCase())) s.hasFocus = true;
  });

  const erids = new Set<string>();
  const advertisers = new Set<string>();
  let bannerRows = 0;
  await adColl.each((a) => {
    bannerRows++;
    if (a.erid) erids.add(a.erid);
    if (a.advertiser_inn) advertisers.add(a.advertiser_inn);
  });

  const suppliers = await supplierNameMap([snapshot]);

  const totalNms = allNms.size;
  const rows: CompetitivenessRow[] = [];
  let hhi = 0;
  for (const [sid, s] of sup) {
    const share = totalNms > 0 ? s.nms.size / totalNms : 0;
    hhi += share * share;
    rows.push({
      supplier_id: sid,
      supplier_name: suppliers.get(sid) ?? '',
      nm_count: s.nms.size,
      share,
      is_focus: s.hasFocus,
    });
  }
  rows.sort((a, b) => b.nm_count - a.nm_count);

  return {
    snapshot,
    query_id: queryId,
    rows,
    summary: {
      total_suppliers: sup.size,
      total_nms: totalNms,
      hhi: Math.round(hhi * 10000) / 10000, // 4 dp — enough precision without float tail
      page1_size: page1Size,
      page1_promo_covered: page1Promo,
      page1_promo_coverage_pct: page1Size > 0 ? Math.round((page1Promo / page1Size) * 100) : 0,
      distinct_banners: erids.size,
      distinct_advertisers: advertisers.size,
      total_banner_rows: bannerRows,
    },
  };
}
