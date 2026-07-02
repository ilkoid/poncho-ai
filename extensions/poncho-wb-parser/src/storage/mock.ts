// src/storage/mock.ts — synthetic captures for testing Decode + the collect loop without the
// browser or live WB. Port of MockIntercepts() in pkg/wbscraper/mock.go.
// Bodies are JS objects (already-parsed — the in-extension interceptor JSON.parse's before forwarding).
//
// The search capture sits on page=2 with dest=8038 so position math is non-trivial
// ((2-1)*100+idx+1), and mixes an organic listing (panelPromoId null) with an ad (panelPromoId 99).

import type { Intercept } from '../db/types';

export function mockIntercepts(): Intercept[] {
  return [
    {
      kind: 'search',
      url: 'https://www.wildberries.ru/catalog/0/search.aspx?search=кроссовки&page=2&dest=8038',
      query_id: 7,
      status: 200,
      body: {
        metadata: { name: 'кроссовки' },
        products: [
          {
            id: 111,
            brand: 'Nike',
            supplierId: 900,
            panelPromoId: null,
            rating: 4.5,
            feedbacks: 10,
            sizes: [{ price: { basic: 100000, product: 89900 } }],
          },
          {
            id: 222,
            brand: 'Adidas',
            supplierId: 901,
            panelPromoId: 99,
            rating: 4.0,
            feedbacks: 5,
            sizes: [{ price: { basic: 50000, product: 45000 } }],
          },
        ],
      },
    },
    {
      kind: 'card_detail',
      url: 'https://www.wildberries.ru/catalog/111/detail.aspx',
      query_id: 7,
      status: 200,
      body: {
        products: [
          {
            id: 111,
            brand: 'Nike',
            supplier: 'ООО Рога',
            supplierId: 900,
            rating: 4.5,
            feedbacks: 10,
            pics: ['a.jpg', 'b.jpg'],
            colors: [{ name: 'черный' }],
            subjectId: 81,
            panelPromoId: null,
            totalQuantity: 250,
            promotions: [{ name: 'Скидка' }],
            sizes: [
              {
                name: '42',
                price: { basic: 100000, product: 89900 },
                stocks: [{ wh: 507, qty: 10, time1: 1720000000, time2: 1720003600 }],
              },
            ],
          },
        ],
      },
    },
  ];
}
