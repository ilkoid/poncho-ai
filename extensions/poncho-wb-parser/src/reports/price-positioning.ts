// src/reports/price-positioning.ts — Ценовое позиционирование. Answers "where is the price core of
// this niche, what corridor should I play in, and does price correlate with the sales proxy
// (feedbacks count)?". Replaces the top-K price view in prices-stocks.ts with a FULL-coverage read
// of search_positions.price_product — every ranked listing, not just the ~detailK opened cards.
//
// Caveat (surfaced in the UI): `price_product` on a position is the listing price for one size row
// (sizes[].price[0]); it does NOT break down by size. Per-size medians still require the top-K
// competitor_card_prices. The listing price is constant per nm_id across pages, so we dedup by nm_id
// (first occurrence wins — same value either way).
//
// Percentile convention: index into sorted-asc, NO interpolation — percentile(p) = sorted[floor((p/100)*(n-1))].
// Crisper than linear interpolation (a bucket-grade report doesn't need sub-step precision) and the
// same row the user can point to in the sorted table. For n=1 every percentile equals that one price.
//
// Deciles: 10 equal-WIDTH price bands (matches the prices-stocks histogram convention). Each band
// carries the median feedbacks of the nms landing in it — a price↔sales-proxy cross-tab that shows
// whether pricier listings in this niche actually accumulate more reviews (a weak sales signal, but
// the best WB storefront data affords without conversion numbers).

import { db } from '../db/dexie';
import { supplierNameMap } from './suppliers';

export interface PriceDecile {
  lo: number; // band lower bound, kopecks
  hi: number; // band upper bound, kopecks
  count: number; // nms whose price falls in [lo, hi]
  median_price: number; // kopecks; median listing price of nms in this band
  median_feedbacks: number; // median feedbacks of nms in this band (sales proxy)
}

export interface PricePositioningRow {
  nm_id: number;
  name: string;
  brand: string;
  supplier_name: string;
  price: number; // kopecks (single listing price_product)
  feedbacks: number;
  rating: number;
  in_corridor: boolean; // p25 <= price <= p75
  decile: number; // 0..9 — which equal-width price band this nm falls in
}

export interface PricePositioningReport {
  snapshot: string;
  query_id: number | null;
  sample_size: number; // distinct nm_ids with a price
  p25: number | null; // kopecks
  median: number | null; // kopecks
  p75: number | null; // kopecks
  majority_lo: number | null; // = p25
  majority_hi: number | null; // = p75
  mode_lo: number | null; // modal bucket bounds (argmax of equal-width histogram)
  mode_hi: number | null;
  deciles: PriceDecile[];
  rows: PricePositioningRow[]; // one per nm, sorted by price asc
  summary: {
    min: number | null; // kopecks
    max: number | null; // kopecks
    in_corridor: number; // count of rows with in_corridor === true
    correlation_price_feedbacks: number | null; // Pearson r, -1..1; null if n < 2 or zero variance
  };
}

interface NmPrice {
  price: number;
  feedbacks: number;
  rating: number;
  name: string;
  brand: string;
  supplier_id: number | null;
}

/** percentile of a sorted-asc array by the index convention (no interpolation). null on empty. */
function percentile(sortedAsc: number[], p: number): number | null {
  if (sortedAsc.length === 0) return null;
  if (sortedAsc.length === 1) return sortedAsc[0]!;
  const idx = Math.floor((p / 100) * (sortedAsc.length - 1));
  return sortedAsc[idx]!;
}

/** median of an unsorted number array by the index convention. 0 on empty (caller guards). */
function medianUnsorted(xs: number[]): number {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.floor((s.length - 1) / 2)]!;
}

/** Pearson correlation via single-pass running sums. null when n < 2 or either variable is constant. */
function pearson(xs: number[], ys: number[]): number | null {
  const n = xs.length;
  if (n < 2) return null;
  let sx = 0;
  let sy = 0;
  let sxx = 0;
  let syy = 0;
  let sxy = 0;
  for (let i = 0; i < n; i++) {
    const x = xs[i]!;
    const y = ys[i]!;
    sx += x;
    sy += y;
    sxx += x * x;
    syy += y * y;
    sxy += x * y;
  }
  const num = n * sxy - sx * sy;
  const den = Math.sqrt((n * sxx - sx * sx) * (n * syy - sy * sy));
  if (den === 0) return null; // zero variance on one axis → r undefined
  return num / den;
}

export async function buildPricePositioning(
  snapshot: string,
  queryId: number | null,
): Promise<PricePositioningReport> {
  const posColl =
    queryId != null
      ? db.search_positions.where('[query_id+snapshot_ts]').equals([queryId, snapshot])
      : db.search_positions.where('snapshot_ts').equals(snapshot);

  // Dedup by nm_id (first wins — listing price is constant per nm across pages/queries).
  const byNm = new Map<number, NmPrice>();
  await posColl.each((r) => {
    if (byNm.has(r.nm_id)) return;
    byNm.set(r.nm_id, {
      price: r.price_product,
      feedbacks: r.feedbacks,
      rating: r.rating,
      name: r.name,
      brand: r.brand,
      supplier_id: r.supplier_id,
    });
  });

  const suppliers = await supplierNameMap([snapshot]);
  // [nm_id, NmPrice] pairs — keep the id so rows carry it without a separate map scan.
  const entries = [...byNm.entries()];
  const n = entries.length;
  const vals = entries.map(([, v]) => v);

  const pricesAsc = vals.map((v) => v.price).sort((a, b) => a - b);
  const p25 = percentile(pricesAsc, 25);
  const median = percentile(pricesAsc, 50);
  const p75 = percentile(pricesAsc, 75);
  const min = n > 0 ? pricesAsc[0]! : null;
  const max = n > 0 ? pricesAsc[pricesAsc.length - 1]! : null;

  // Equal-width price bands (10) — same convention as prices-stocks histogram. argmax = mode.
  const span = n > 0 ? Math.max(1, max! - min!) : 1;
  const w = n > 0 ? span / 10 : 1;
  const bandOf = (price: number): number => {
    if (n === 0) return 0;
    let b = Math.floor((price - min!) / w);
    if (b > 9) b = 9;
    if (b < 0) b = 0;
    return b;
  };

  // Build deciles (bands) + locate the modal band.
  const bandCounts = new Array<number>(10).fill(0);
  const bandFeedbacks: number[][] = Array.from({ length: 10 }, () => []);
  const bandPrices: number[][] = Array.from({ length: 10 }, () => []);
  for (const [, v] of entries) {
    const b = bandOf(v.price);
    bandCounts[b] = (bandCounts[b] ?? 0) + 1;
    bandFeedbacks[b]!.push(v.feedbacks);
    bandPrices[b]!.push(v.price);
  }
  let modeBand = 0;
  for (let b = 1; b < 10; b++) if (bandCounts[b]! > bandCounts[modeBand]!) modeBand = b;

  const deciles: PriceDecile[] = [];
  if (n > 0) {
    for (let b = 0; b < 10; b++) {
      // Keep all 10 bands for a stable chart even when some are empty.
      deciles.push({
        lo: Math.round(min! + b * w),
        hi: Math.round(min! + (b + 1) * w),
        count: bandCounts[b]!,
        median_price: medianUnsorted(bandPrices[b]!),
        median_feedbacks: medianUnsorted(bandFeedbacks[b]!),
      });
    }
  } // n === 0 → deciles stays empty (render + export early-return on sample_size === 0)

  // Rows + in_corridor flag + correlation.
  const correlation = pearson(
    vals.map((v) => v.price),
    vals.map((v) => v.feedbacks),
  );
  let inCorridor = 0;
  const rows: PricePositioningRow[] = entries.map(([nmId, v]) => {
    const inc = p25 != null && p75 != null && v.price >= p25 && v.price <= p75;
    if (inc) inCorridor++;
    return {
      nm_id: nmId,
      name: v.name,
      brand: v.brand,
      supplier_name: v.supplier_id != null ? (suppliers.get(v.supplier_id) ?? '') : '',
      price: v.price,
      feedbacks: v.feedbacks,
      rating: v.rating,
      in_corridor: inc,
      decile: bandOf(v.price),
    };
  });
  rows.sort((a, b) => a.price - b.price);

  return {
    snapshot,
    query_id: queryId,
    sample_size: n,
    p25,
    median,
    p75,
    majority_lo: p25,
    majority_hi: p75,
    mode_lo: n > 0 ? deciles[modeBand]!.lo : null,
    mode_hi: n > 0 ? deciles[modeBand]!.hi : null,
    deciles,
    rows,
    summary: {
      min,
      max,
      in_corridor: inCorridor,
      correlation_price_feedbacks: correlation,
    },
  };
}
