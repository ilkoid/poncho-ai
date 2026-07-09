// src/decode/index.ts — the Decode router. Ports Decode() in pkg/wbscraper/decode.go.
//
// Routes one captured WB response (Intercept) to its per-table rows by `kind`:
//   search | brand    → search_positions
//   card_list         → competitor_cards + competitor_card_prices
//   card_detail       → competitor_cards + prices + competitor_card_details + competitor_card_stocks
//   card_content      → competitor_card_meta + competitor_card_options (wbbasket.ru CDN card.json)
//   ad                → vitrine_ads
//   (anything else)   → empty Decoded, no error (a new WB endpoint must never break the pipeline).
//
// search/card/content throw on a non-object body (malformed capture → caller logs it); ad never throws.

import type { Decoded, Intercept, SnapshotTs } from '../db/types';
import { decodeCard } from './card';
import { decodeCardContent } from './content';
import { decodeAd } from './ad';
import { decodeSearch } from './search';

export { WB_PAGE_SIZE, pageAndDest } from './search';
export { parseOrdMark, eridFromHref } from './ad';

/** Decode routes one Intercept to its per-table rows. QueryID propagates from the Intercept into
 *  every row (provenance binding). Unknown kinds → empty Decoded + no error. */
export function Decode(it: Intercept, snapshot: SnapshotTs): Decoded {
  switch (it.kind) {
    case 'search':
    case 'brand':
      return { ...EMPTY_DECODED, search_positions: decodeSearch(it, snapshot) };
    case 'card_list':
      return toDecoded(decodeCard(it, snapshot, false), false);
    case 'card_detail':
      return toDecoded(decodeCard(it, snapshot, true), true);
    case 'card_content':
      return { ...EMPTY_DECODED, ...decodeCardContent(it, snapshot) };
    case 'ad':
      return { ...EMPTY_DECODED, vitrine_ads: decodeAd(it, snapshot) };
    default:
      return EMPTY_DECODED;
  }
}

const EMPTY_DECODED: Decoded = {
  search_positions: [],
  vitrine_ads: [],
  competitor_cards: [],
  competitor_card_prices: [],
  competitor_card_details: [],
  competitor_card_stocks: [],
  competitor_card_meta: [],
  competitor_card_options: [],
  competitor_card_compositions: [],
  competitor_card_sizes: [],
  competitor_card_colors: [],
};

/** Packs the card decoder's tuple-shaped return into a Decoded bundle. */
function toDecoded(
  c: ReturnType<typeof decodeCard>,
  detail: boolean,
): Decoded {
  return {
    search_positions: [],
    vitrine_ads: [],
    competitor_cards: c.competitor_cards,
    competitor_card_prices: c.competitor_card_prices,
    competitor_card_details: detail ? c.competitor_card_details : [],
    competitor_card_stocks: detail ? c.competitor_card_stocks : [],
    competitor_card_meta: [],
    competitor_card_options: [],
    competitor_card_compositions: [],
    competitor_card_sizes: [],
    competitor_card_colors: [],
  };
}
