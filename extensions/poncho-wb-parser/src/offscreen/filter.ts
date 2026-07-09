// src/offscreen/filter.ts — pure (chrome/DB-free) helper that drops carousel/recommendation
// competitor cards before they reach Dexie.
//
// Why this exists: when the detail-harvest opens a card page, WB's SPA also fires /cards/vN/list
// for the "похожие/рекомендованные" carousels. Those captures inherit the active query's query_id
// (the SW stamps one coarse currentQueryId per target — see background/sw.ts onIntercept), so a
// "трусы для девочки" snapshot ends up carrying school-shoe cards. Because pickHarvestNmids() only
// ever navigates nm_ids that ranked (search_positions), a card whose nm is NOT in this snapshot's
// positions AND carries a real query_id is, by construction, carousel noise — not a competitor that
// ranked. query_id=null rows are direct nmId/url targets, whose carousels ARE legitimate competitor
// context, so they are always kept.
//
// The filter runs in decodeAndPersist BEFORE dedupeBySeen: carousel nm must never enter seen.cards,
// or a later re-capture of the same carousel nm would be treated as "already seen" and slip past.

import type { Decoded } from '../db/types';

/**
 * Drop competitor_card rows (and their cascade children: prices, details, stocks) whose nm_id is not
 * a ranked position for this snapshot. Pass an empty set when no positions have been observed yet —
 * the filter then passes everything through (we cannot judge validity without positions, and the
 * carousel arrives only after the search phase, so in practice the set is populated by then).
 */
export function dropCascadeCards(d: Decoded, positionNm: ReadonlySet<number>): Decoded {
  if (positionNm.size === 0) return d; // can't judge — safe passthrough
  const keep = (qid: number | null, nm: number): boolean => qid == null || positionNm.has(nm);
  return {
    ...d,
    competitor_cards: d.competitor_cards.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_prices: d.competitor_card_prices.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_details: d.competitor_card_details.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_stocks: d.competitor_card_stocks.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_meta: d.competitor_card_meta.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_options: d.competitor_card_options.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_compositions: d.competitor_card_compositions.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_sizes: d.competitor_card_sizes.filter((r) => keep(r.query_id, r.nm_id)),
    competitor_card_colors: d.competitor_card_colors.filter((r) => keep(r.query_id, r.nm_id)),
  };
}
