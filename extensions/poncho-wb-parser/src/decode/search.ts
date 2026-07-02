// src/decode/search.ts — decode search/brand responses into SearchPosition rows.
// Port of decodeSearch + pageAndDest + firstSizePrice in pkg/wbscraper/decode.go.

import type { Intercept, SearchPosition, SnapshotTs } from '../db/types';
import type { WBSearchResponse, WBSize } from './shapes';
import { asObject } from './helpers';

/** WB returns 100 products per search page; the global rank is (page-1)*100 + index + 1. */
export const WB_PAGE_SIZE = 100;

const RE_PAGE = /[?&]page=(\d+)/;
const RE_DEST = /[?&]dest=(\d+)/;

/** Extract page (default 1, only if > 0) and dest/region (null if absent) from a WB search URL. */
export function pageAndDest(url: string): { page: number; dest: number | null } {
  let page = 1;
  let dest: number | null = null;
  const pm = url.match(RE_PAGE);
  if (pm?.[1]) {
    const n = Number(pm[1]);
    if (Number.isFinite(n) && n > 0) page = n;
  }
  const dm = url.match(RE_DEST);
  if (dm?.[1]) {
    const n = Number(dm[1]);
    if (Number.isFinite(n)) dest = n;
  }
  return { page, dest };
}

/** Representative first-size price (kopecks). WB search carries it under sizes[0].price. */
function firstSizePrice(sizes: WBSize[] | undefined): { basic: number; product: number } {
  if (!sizes || sizes.length === 0 || !sizes[0]?.price) return { basic: 0, product: 0 };
  return { basic: sizes[0].price.basic ?? 0, product: sizes[0].price.product ?? 0 };
}

/** decodeSearch flattens products into one SearchPosition per product. resultset="filters" and
 *  null/absent products produce nothing. Throws on a non-object body (malformed capture). */
export function decodeSearch(it: Intercept, snapshot: SnapshotTs): SearchPosition[] {
  const resp = asObject(it.body, 'search') as unknown as WBSearchResponse;
  if (resp.resultset != null && resp.resultset.toLowerCase() === 'filters') {
    return []; // facet-only response (products is null) — skip
  }
  const { page, dest } = pageAndDest(it.url);
  const out: SearchPosition[] = [];
  const products = resp.products ?? [];
  for (let idx = 0; idx < products.length; idx++) {
    const p = products[idx];
    if (!p) continue; // sparse slot — defensive, matches background.js `if (!p) return`
    const { basic, product } = firstSizePrice(p.sizes);
    out.push({
      snapshot_ts: snapshot,
      query_id: it.query_id,
      region_dest: dest,
      page,
      position: (page - 1) * WB_PAGE_SIZE + idx + 1,
      nm_id: p.id,
      name: p.name ?? '',
      brand: p.brand ?? '',
      supplier_id: p.supplierId ?? null,
      panel_promo_id: p.panelPromoId ?? null,
      price_basic: basic,
      price_product: product,
      rating: p.rating ?? 0,
      feedbacks: p.feedbacks ?? 0,
    });
  }
  return out;
}
