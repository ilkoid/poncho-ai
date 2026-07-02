// tests/write.dedup.test.ts — dedupeBySeen: drops fact rows whose natural key was already seen in
// this snapshot. This is the fix for the duplicate cards/details/prices/stocks found in the live
// dump (WB SPA re-fires /list+/detail on scroll/nav; bulkAdd is append-only). Pure unit test — no
// chrome, no DB.

import { describe, it, expect } from 'vitest';
import { dedupeBySeen, freshSeen } from '../src/db/write';
import type { Decoded } from '../src/db/types';

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
});
