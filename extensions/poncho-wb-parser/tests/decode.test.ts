// tests/decode.test.ts — the decode regression barrier. A faithful port of pkg/wbscraper/decode_test.go.
//
// The `raw` helper lifts the Go test JSON verbatim: valid JSON → parsed object (what Decode sees in
// v2, where the interceptor JSON.parse's before forwarding); invalid JSON ('{not json') → the raw
// string, which Decode rejects via asObject (preserving Go's "malformed body → error" contract).

import { describe, it, expect } from 'vitest';
import { Decode } from '../src/decode';
import { mockIntercepts } from '../src/storage/mock';
import type { Intercept, SnapshotTs } from '../src/db/types';

const ts: SnapshotTs = '2026-07-01T10:00:00Z';

function safeParse(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return s; // malformed → return the raw string so Decode sees a non-object and throws
  }
}

function raw(kind: string, url: string, body: string): Intercept {
  return { kind, url, query_id: 7, status: 200, body: safeParse(body) };
}

describe('Decode — search', () => {
  it('position math (page-1)*100+idx+1 and kopecks survive verbatim', () => {
    const d = Decode(
      raw('search', 'https://w.ru/s?search=x&page=2&dest=8038', `{
        "products": [
          {"id":111,"sizes":[{"price":{"basic":100000,"product":89900}}]},
          {"id":222,"sizes":[{"price":{"basic":50000,"product":45000}}]}
        ]}`),
      ts,
    );
    expect(d.search_positions).toHaveLength(2);
    const want = [
      { pos: 101, nm: 111, basic: 100000, prod: 89900, region: 8038 },
      { pos: 102, nm: 222, basic: 50000, prod: 45000, region: 8038 },
    ];
    for (let i = 0; i < want.length; i++) {
      const r = d.search_positions[i]!;
      const w = want[i]!;
      expect(r.position).toBe(w.pos);
      expect(r.nm_id).toBe(w.nm);
      expect(r.price_basic).toBe(w.basic);
      expect(r.price_product).toBe(w.prod);
      expect(r.region_dest).toBe(w.region);
      expect(r.page).toBe(2);
    }
  });

  it('panel_promo_id null → organic, non-null → ad', () => {
    const d = Decode(
      raw('search', 'https://w.ru/s?page=1', `{
        "products": [{"id":1,"panelPromoId":null},{"id":2,"panelPromoId":555}]}`),
      ts,
    );
    expect(d.search_positions[0]!.panel_promo_id).toBeNull();
    expect(d.search_positions[1]!.panel_promo_id).toBe(555);
  });

  it('resultset=filters (products null) is skipped entirely', () => {
    const d = Decode(raw('search', 'https://w.ru/s?page=1', `{"resultset":"filters","products":null}`), ts);
    expect(d.search_positions).toHaveLength(0);
  });

  it('propagates the Intercept query_id onto every row', () => {
    const d = Decode(
      { kind: 'search', url: 'https://w.ru/s?page=1', query_id: 42, status: 200, body: safeParse(`{"products":[{"id":1},{"id":2}]}`) },
      ts,
    );
    expect(d.search_positions.every((r) => r.query_id === 42)).toBe(true);
  });
});

describe('Decode — card', () => {
  it('/list produces cards + per-size prices, no details/stocks', () => {
    const d = Decode(
      raw('card_list', 'https://w.ru/l', `{
        "products": [{
          "id":111,"brand":"Nike","supplier":"ООО Рога","supplierId":900,"rating":4.5,"feedbacks":10,
          "pics":["a.jpg","b.jpg"],"colors":[{"name":"черный"}],"subjectId":81,
          "sizes":[{"name":"42","price":{"basic":100000,"product":89900}},
                   {"name":"43","price":{"basic":110000,"product":99000}}]
        }]}`),
      ts,
    );
    expect(d.competitor_cards).toHaveLength(1);
    const c = d.competitor_cards[0]!;
    expect(c.nm_id).toBe(111);
    expect(c.brand).toBe('Nike');
    expect(c.supplier).toBe('ООО Рога');
    expect(c.pics).toBe(2);
    expect(c.colors).toBe('черный');
    expect(c.query_id).toBe(7);
    expect(d.competitor_card_prices).toHaveLength(2);
    expect(d.competitor_card_prices[0]!.price_product).toBe(89900);
    expect(d.competitor_card_prices[1]!.price_product).toBe(99000);
    expect(d.competitor_card_details).toHaveLength(0);
    expect(d.competitor_card_stocks).toHaveLength(0);
  });

  it('/detail adds aggregate details + per-wh stocks', () => {
    const d = Decode(
      raw('card_detail', 'https://w.ru/d', `{
        "products": [{
          "id":111,"brand":"Nike","totalQuantity":250,"promotions":[{"name":"Скидка"}],
          "sizes":[{"name":"42","price":{"basic":100000,"product":89900},
                    "stocks":[{"wh":507,"qty":10,"time1":1720000000,"time2":1720003600}]}]
        }]}`),
      ts,
    );
    expect(d.competitor_cards).toHaveLength(1);
    expect(d.competitor_card_prices).toHaveLength(1);
    expect(d.competitor_card_details).toHaveLength(1);
    const det = d.competitor_card_details[0]!;
    expect(det.total_quantity).toBe(250);
    expect(det.promotions).not.toBe('');
    expect(d.competitor_card_stocks).toHaveLength(1);
    const st = d.competitor_card_stocks[0]!;
    expect(st.wh_id).toBe(507);
    expect(st.qty).toBe(10);
  });
});

describe('Decode — ad', () => {
  it('v2 banners array: social ad (ОРД), internal promo (promoText), bare link (alt)', () => {
    const d = Decode(
      raw('ad', 'https://banners-website.wildberries.ru/public/v2/banners?urltype=1024', `[
        {"href":"https://projects.pervye.ru/?utm=x&erid=L71GTkMSi","src":"/adsf/1782856217708260210.webp",
         "alt":"Социальная реклама","ordBannerMark":"ДВИЖЕНИЕ ПЕРВЫХ, ИНН 9709087880, ЕРИД L71GTkMSi","bannerType":"static"},
        {"href":"/promotions/vse-dlya-uborki","src":"/poster/ru/action2/c660x210/tab_hozztov_12_22574745.jpg",
         "alt":"Хозяйственные товары","promoText":"Хозяйственные товары","bannerType":"static"},
        {"href":"/wbclub","src":"/poster/ru/horizontal1/960x412/960x412.jpg",
         "alt":"Wb Клуб","bannerType":""}
      ]`),
      ts,
    );
    expect(d.vitrine_ads).toHaveLength(3);
    const a = d.vitrine_ads[0]!;
    expect(a.advertiser_name).toBe('ДВИЖЕНИЕ ПЕРВЫХ');
    expect(a.advertiser_inn).toBe('9709087880');
    expect(a.erid).toBe('L71GTkMSi');
    expect(a.banner_type).toBe('static');
    expect(a.creative_url).toBe('/adsf/1782856217708260210.webp');
    expect(a.landing_href).toBe('https://projects.pervye.ru/?utm=x&erid=L71GTkMSi');
    expect(a.promo_id).toBeNull();
    const b = d.vitrine_ads[1]!;
    expect(b.advertiser_name).toBe('Хозяйственные товары');
    expect(b.advertiser_inn).toBe('');
    expect(b.erid).toBe('');
    expect(d.vitrine_ads[2]!.advertiser_name).toBe('Wb Клуб');
  });

  it('shelfs/search object: both slots populated → 2 ads', () => {
    const d = Decode(
      raw('ad', '/__internal/banners/shelfs/search?query=x', `{
        "metadata":{"query":"x"},
        "data":{
          "banners":{"data":[{"href":"/b1","src":"/s1.jpg","alt":"B1","bannerType":"static"}],"total":1},
          "shelfs":{"data":[{"href":"/b2","src":"/s2.jpg","alt":"B2",
            "ordBannerMark":"ООО ТЕСТ, ИНН 1234567890, ЕРИД Lj1"}],"total":1}}}`),
      ts,
    );
    expect(d.vitrine_ads).toHaveLength(2);
    expect(d.vitrine_ads[0]!.advertiser_name).toBe('B1');
    const s = d.vitrine_ads[1]!;
    expect(s.advertiser_name).toBe('ООО ТЕСТ');
    expect(s.advertiser_inn).toBe('1234567890');
    expect(s.erid).toBe('Lj1');
  });

  it('shelfs/search object: both slots empty → 0 ads', () => {
    const d = Decode(
      raw('ad', '/__internal/banners/shelfs/search?query=x', `{
        "metadata":{"query":"x"},
        "data":{"banners":{"data":[],"total":0},"shelfs":{"data":[],"total":0}}}`),
      ts,
    );
    expect(d.vitrine_ads).toHaveLength(0);
  });

  it('empty array yields nothing (no error)', () => {
    const d = Decode(raw('ad', 'https://banners-website.wildberries.ru/public/v2/banners', `[]`), ts);
    expect(d.vitrine_ads).toHaveLength(0);
  });

  it('erid falls back to the landing href ?erid= param', () => {
    const d = Decode(
      raw('ad', 'https://banners-website.wildberries.ru/public/v2/banners', `[
        {"href":"https://adv.example/?erid=Lj9","src":"/s.jpg","alt":"A",
         "ordBannerMark":"ООО ФИРМА, ИНН 9999999999"}]`),
      ts,
    );
    const a = d.vitrine_ads[0]!;
    expect(a.advertiser_name).toBe('ООО ФИРМА');
    expect(a.advertiser_inn).toBe('9999999999');
    expect(a.erid).toBe('Lj9');
  });
});

describe('Decode — robustness', () => {
  it('unknown kind yields no rows and no error', () => {
    const d = Decode({ kind: 'mystery', url: 'https://w.ru/x', query_id: null, status: 200, body: safeParse(`{}`) }, ts);
    expect(
      d.search_positions.length +
        d.vitrine_ads.length +
        d.competitor_cards.length +
        d.competitor_card_prices.length +
        d.competitor_card_details.length +
        d.competitor_card_stocks.length,
    ).toBe(0);
  });

  it('malformed body returns an error (not a silent empty result)', () => {
    expect(() => Decode(raw('search', 'https://w.ru/s', `{not json`), ts)).toThrow();
  });
});

describe('mockIntercepts', () => {
  it('decodes to the expected rows', () => {
    const caps = mockIntercepts();
    expect(caps).toHaveLength(2);
    const dSearch = Decode(caps[0]!, ts);
    expect(dSearch.search_positions).toHaveLength(2);
    expect(dSearch.search_positions[0]!.position).toBe(101);
    expect(dSearch.search_positions[1]!.position).toBe(102);
    expect(dSearch.search_positions[0]!.price_product).toBe(89900);
    expect(dSearch.search_positions[1]!.panel_promo_id).toBe(99);
    const dCard = Decode(caps[1]!, ts);
    expect(dCard.competitor_cards).toHaveLength(1);
    expect(dCard.competitor_card_stocks).toHaveLength(1);
    expect(dCard.competitor_card_details).toHaveLength(1);
  });
});
