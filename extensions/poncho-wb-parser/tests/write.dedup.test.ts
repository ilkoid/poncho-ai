// tests/write.dedup.test.ts — dedupeBySeen: drops fact rows whose natural key was already seen in
// this snapshot. This is the fix for the duplicate cards/details/prices/stocks found in the live
// dump (WB SPA re-fires /list+/detail on scroll/nav; bulkAdd is append-only). Pure unit test — no
// chrome, no DB.

import { describe, it, expect } from 'vitest';
import { dedupeBySeen, freshSeen } from '../src/db/write';
import type { CompetitorCardColor, CompetitorCardComposition, CompetitorCardMeta, CompetitorCardOption, CompetitorCardSize, Decoded } from '../src/db/types';

const S = '2026-07-02T17:54:05Z';

/** A bundle with one distinct card plus its 4 duplicated sibling rows (each non-position table has
 *  the same natural key twice). */
function bundle(): Decoded {
  return {
    search_positions: [
      { snapshot_ts: S, query_id: 1, region_dest: 1, page: 1, position: 1, nm_id: 1, name: 'A', brand: 'A', supplier_id: 1, panel_promo_id: null, price_basic: 0, price_product: 0, rating: 0, feedbacks: 0 },
    ],
    vitrine_ads: [
      { snapshot_ts: S, query_id: 1, advertiser_name: 'X', advertiser_inn: '1', erid: 'e', promo_id: null, banner_type: 't', creative_url: 'c', landing_href: 'l' },
    ],
    competitor_cards: [
      { snapshot_ts: S, query_id: 1, nm_id: 10, name: 'B', brand: 'B', supplier: 'S', supplier_id: 2, rating: 5, feedbacks: 1, pics: 1, weight: 0.1, volume: 1, colors: '', subject_id: 1, panel_promo_id: null },
      { snapshot_ts: S, query_id: 1, nm_id: 10, name: 'B', brand: 'B', supplier: 'S', supplier_id: 2, rating: 5, feedbacks: 1, pics: 1, weight: 0.1, volume: 1, colors: '', subject_id: 1, panel_promo_id: null },
    ],
    competitor_card_prices: [
      { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', price_basic: 100, price_product: 90, wh_id: 507 },
      { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', price_basic: 100, price_product: 90, wh_id: 507 },
    ],
    competitor_card_details: [
      { snapshot_ts: S, query_id: 1, nm_id: 10, total_quantity: 5, promotions: '' },
      { snapshot_ts: S, query_id: 1, nm_id: 10, total_quantity: 5, promotions: '' },
    ],
    competitor_card_stocks: [
      { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', wh_id: 507, qty: 3, time1: 1, time2: 2 },
      { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', wh_id: 507, qty: 3, time1: 1, time2: 2 },
    ],
    competitor_card_meta: [],
    competitor_card_options: [],
    competitor_card_compositions: [],
    competitor_card_sizes: [],
    competitor_card_colors: [],
  };
}

describe('dedupeBySeen', () => {
  it('drops duplicate natural keys within one bundle (first wins)', () => {
    const out = dedupeBySeen(bundle(), freshSeen());
    expect(out.competitor_cards).toHaveLength(1);
    expect(out.competitor_card_prices).toHaveLength(1);
    expect(out.competitor_card_details).toHaveLength(1);
    expect(out.competitor_card_stocks).toHaveLength(1);
    expect(out.search_positions).toHaveLength(1);
    expect(out.vitrine_ads).toHaveLength(1); // untouched (no stable per-row key)
  });

  it('records keys so a second identical bundle yields nothing new (cross-intercept dedup)', () => {
    const seen = freshSeen();
    dedupeBySeen(bundle(), seen);
    const out2 = dedupeBySeen(bundle(), seen);
    expect(out2.competitor_cards).toHaveLength(0);
    expect(out2.competitor_card_prices).toHaveLength(0);
    expect(out2.competitor_card_details).toHaveLength(0);
    expect(out2.competitor_card_stocks).toHaveLength(0);
    expect(out2.search_positions).toHaveLength(0);
  });

  it('keeps rows with the same nm_id but different size/wh (genuinely distinct facts)', () => {
    const d: Decoded = {
      ...bundle(),
      competitor_card_prices: [
        { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', price_basic: 100, price_product: 90, wh_id: 507 },
        { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '44', price_basic: 110, price_product: 99, wh_id: 507 },
        { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', price_basic: 100, price_product: 90, wh_id: 208 },
      ],
      competitor_card_stocks: [
        { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', wh_id: 507, qty: 3, time1: 1, time2: 2 },
        { snapshot_ts: S, query_id: 1, nm_id: 10, size_name: '42', wh_id: 208, qty: 1, time1: 3, time2: 4 },
      ],
    };
    const out = dedupeBySeen(d, freshSeen());
    expect(out.competitor_card_prices).toHaveLength(3); // 2 sizes × wh mix, all distinct
    expect(out.competitor_card_stocks).toHaveLength(2); // same size, different wh
  });

  it('freshSeen starts a clean slate (a new snapshot does not inherit prior keys)', () => {
    const seen = freshSeen();
    dedupeBySeen(bundle(), seen);
    expect(seen.cards.size).toBe(1);
    const out = dedupeBySeen(bundle(), freshSeen()); // brand-new snapshot
    expect(out.competitor_cards).toHaveLength(1); // not deduped against the previous snapshot
  });

  it('keeps the same nm|page across DIFFERENT queries/regions (regression: cross-query dedup)', () => {
    // Bug: the positions dedup key used to be `${nm_id}|${page}`, omitting query_id + region_dest.
    // Popular nms re-rank on the same page across many similar queries → Q2..N positions were dropped
    // → reports showed "only the first query". The natural key is (query_id, region_dest, page, nm_id);
    // within-query /search re-fires (identical key) are still deduped.
    const pos = (query_id: number, region_dest: number | null, nm_id: number, page: number) =>
      ({
        snapshot_ts: S, query_id, region_dest, page, position: 1, nm_id, name: 'A', brand: 'A',
        supplier_id: 1, panel_promo_id: null, price_basic: 0, price_product: 0, rating: 0, feedbacks: 0,
      });
    const d: Decoded = {
      ...bundle(),
      search_positions: [
        pos(1, 8038, 111, 1), // Q1, region 8038, nm 111, page 1
        pos(2, 8038, 111, 1), // Q2 — same nm, same page, same region, DIFFERENT query → distinct
        pos(1, 8038, 111, 1), // Q1 re-fire → identical key → dropped
        pos(1, 77, 111, 1), // Q1 — same nm+page, DIFFERENT region → distinct
      ],
    };
    const out = dedupeBySeen(d, freshSeen());
    expect(out.search_positions).toHaveLength(3); // Q1@8038, Q2@8038, Q1@77 — only the re-fire dropped
    const keys = out.search_positions.map((r) => `${r.query_id}|${r.region_dest}`);
    expect(keys).toEqual(expect.arrayContaining(['1|8038', '2|8038', '1|77']));
  });

  it('dedups competitor_card_meta by nm_id, options by nm_id|char_name, and the 3 new EAV tables by their natural keys', () => {
    const opt = (nm_id: number, char_name: string): CompetitorCardOption =>
      ({ snapshot_ts: S, query_id: 1, nm_id, char_name, char_value: 'v', charc_type: 1, is_variable: 0, variable_values: '', group_name: '' });
    const meta = (nm_id: number): CompetitorCardMeta =>
      ({ snapshot_ts: S, query_id: 1, nm_id, vendor_code: 'V', subj_name: '', subj_root_name: '', description: '', need_kiz: 0, create_date: '', update_date: '', imt_id: null, imt_name: '', slug: '', brand_name: '', brand_hash: '', supplier_id: null, photo_count: 0, has_video: 0, subject_id: null, subject_root_id: null, nm_colors_names: '', contents: '', has_seller_recommendations: 0, user_flags: 0, kinds: '' });
    const comp = (nm_id: number, name: string): CompetitorCardComposition =>
      ({ snapshot_ts: S, query_id: 1, nm_id, name, ord: 0 });
    const sz = (nm_id: number, tech_size: string, prop_name: string): CompetitorCardSize =>
      ({ snapshot_ts: S, query_id: 1, nm_id, tech_size, chrt_id: null, prop_name, prop_value: 'v', prop_order: 0 });
    const clr = (nm_id: number, color_nm_id: number): CompetitorCardColor =>
      ({ snapshot_ts: S, query_id: 1, nm_id, color_nm_id, ord: 0 });
    const d: Decoded = {
      ...bundle(),
      competitor_card_meta: [meta(10), meta(10), meta(11)],
      competitor_card_options: [opt(10, 'Состав'), opt(10, 'Состав'), opt(10, 'Цвет'), opt(11, 'Состав')],
      competitor_card_compositions: [comp(10, 'хлопок'), comp(10, 'хлопок'), comp(10, 'полиэстер'), comp(11, 'хлопок')],
      competitor_card_sizes: [sz(10, '42', 'RU'), sz(10, '42', 'RU'), sz(10, '42', 'Рост'), sz(11, '42', 'RU')],
      competitor_card_colors: [clr(10, 1), clr(10, 1), clr(10, 2), clr(11, 1)],
    };
    const out = dedupeBySeen(d, freshSeen());
    expect(out.competitor_card_meta).toHaveLength(2); // nm 10 (once) + nm 11
    expect(out.competitor_card_options).toHaveLength(3); // 10|Состав, 10|Цвет, 11|Состав
    expect(out.competitor_card_compositions).toHaveLength(3); // 10|хлопок, 10|полиэстер, 11|хлопок
    expect(out.competitor_card_sizes).toHaveLength(3); // 10|42|RU, 10|42|Рост, 11|42|RU
    expect(out.competitor_card_colors).toHaveLength(3); // 10|1, 10|2, 11|1
  });
});
