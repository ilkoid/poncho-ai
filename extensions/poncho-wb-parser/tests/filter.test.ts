// tests/filter.test.ts — dropCascadeCards: pure carousel-filter for competitor cards.
// No DB, no chrome — just the Decoded bundle in / filtered bundle out. Mirrors the offscreen
// helper that stops carousel/recommendation captures from polluting a query's competitor set.

import { describe, it, expect } from 'vitest';
import { dropCascadeCards } from '../src/offscreen/filter';
import type {
  Decoded,
  CompetitorCard,
  CompetitorCardPrice,
  CompetitorCardDetail,
  CompetitorCardStock,
} from '../src/db/types';

const SNAP = '2026-07-03T08:54:17.934Z';

function card(qid: number | null, nm: number): CompetitorCard {
  return {
    snapshot_ts: SNAP, query_id: qid, nm_id: nm, name: '', brand: '', supplier: '', supplier_id: null,
    rating: 0, feedbacks: 0, pics: 0, weight: 0, volume: 0, colors: '', subject_id: null, panel_promo_id: null,
  };
}
function price(qid: number | null, nm: number): CompetitorCardPrice {
  return { snapshot_ts: SNAP, query_id: qid, nm_id: nm, size_name: '', price_basic: 0, price_product: 0, wh_id: null };
}
function detail(qid: number | null, nm: number): CompetitorCardDetail {
  return { snapshot_ts: SNAP, query_id: qid, nm_id: nm, total_quantity: 0, promotions: '' };
}
function stock(qid: number | null, nm: number): CompetitorCardStock {
  return { snapshot_ts: SNAP, query_id: qid, nm_id: nm, size_name: '', wh_id: null, qty: 0, time1: null, time2: null };
}
function bundle(
  cards: CompetitorCard[],
  prices: CompetitorCardPrice[] = [],
  details: CompetitorCardDetail[] = [],
  stocks: CompetitorCardStock[] = [],
): Decoded {
  return {
    search_positions: [], vitrine_ads: [],
    competitor_cards: cards, competitor_card_prices: prices, competitor_card_details: details, competitor_card_stocks: stocks,
    competitor_card_meta: [], competitor_card_options: [],
    competitor_card_compositions: [], competitor_card_sizes: [], competitor_card_colors: [],
  };
}

describe('dropCascadeCards', () => {
  it('passes everything through when no positions observed yet (safe default)', () => {
    const d = bundle([card(28, 111), card(28, 999)]); // 999 not ranked, but positionNm empty → can't judge
    const out = dropCascadeCards(d, new Set());
    expect(out.competitor_cards).toHaveLength(2);
  });

  it('keeps ranked cards (qid!=null, nm in positions) and drops carousel cards (nm not in positions)', () => {
    const d = bundle([card(28, 111), card(28, 999)]); // 111 ranked, 999 = carousel shoe
    const out = dropCascadeCards(d, new Set([111]));
    expect(out.competitor_cards.map((c) => c.nm_id)).toEqual([111]);
  });

  it('keeps direct-target cards (query_id=null) even when nm is not in positions', () => {
    const d = bundle([card(null, 555)]); // direct nmId target, not a ranked position
    const out = dropCascadeCards(d, new Set([111]));
    expect(out.competitor_cards.map((c) => c.nm_id)).toEqual([555]);
  });

  it('drops the cascade children (prices/details/stocks/compositions/sizes/colors) together with the card', () => {
    const comp = (qid: number | null, nm: number) => ({ snapshot_ts: SNAP, query_id: qid, nm_id: nm, name: 'хлопок', ord: 0 });
    const sz = (qid: number | null, nm: number) => ({ snapshot_ts: SNAP, query_id: qid, nm_id: nm, tech_size: '42', chrt_id: null, prop_name: 'RU', prop_value: '42', prop_order: 0 });
    const clr = (qid: number | null, nm: number) => ({ snapshot_ts: SNAP, query_id: qid, nm_id: nm, color_nm_id: 777, ord: 0 });
    const d: Decoded = {
      ...bundle(
        [card(28, 111), card(28, 999)],
        [price(28, 111), price(28, 999)],
        [detail(28, 111), detail(28, 999)],
        [stock(28, 111), stock(28, 999)],
      ),
      competitor_card_compositions: [comp(28, 111), comp(28, 999)],
      competitor_card_sizes: [sz(28, 111), sz(28, 999)],
      competitor_card_colors: [clr(28, 111), clr(28, 999)],
    };
    const out = dropCascadeCards(d, new Set([111]));
    expect(out.competitor_cards.map((c) => c.nm_id)).toEqual([111]);
    expect(out.competitor_card_prices.map((p) => p.nm_id)).toEqual([111]);
    expect(out.competitor_card_details.map((p) => p.nm_id)).toEqual([111]);
    expect(out.competitor_card_stocks.map((p) => p.nm_id)).toEqual([111]);
    expect(out.competitor_card_compositions.map((p) => p.nm_id)).toEqual([111]);
    expect(out.competitor_card_sizes.map((p) => p.nm_id)).toEqual([111]);
    expect(out.competitor_card_colors.map((p) => p.nm_id)).toEqual([111]);
  });

  it('leaves search_positions and vitrine_ads untouched', () => {
    const d: Decoded = {
      ...bundle([card(28, 999)]),
      search_positions: [
        { snapshot_ts: SNAP, query_id: 28, region_dest: null, page: 1, position: 1, nm_id: 111, name: '', brand: '', supplier_id: null, panel_promo_id: null, price_basic: 0, price_product: 0, rating: 0, feedbacks: 0 },
      ],
      vitrine_ads: [
        { snapshot_ts: SNAP, query_id: 28, advertiser_name: '', advertiser_inn: '', erid: '', promo_id: null, banner_type: '', creative_url: '', landing_href: '' },
      ],
    };
    const out = dropCascadeCards(d, new Set([111]));
    expect(out.search_positions).toHaveLength(1);
    expect(out.vitrine_ads).toHaveLength(1);
  });
});
