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
    {
      // wbbasket.ru static CDN card.json — flat content object (description + characteristics).
      // nm_id is in the body (authoritative); the URL path also encodes it as a fallback.
      kind: 'card_content',
      url: 'https://basket-10.wbbasket.ru/vol1/part111/111/info/ru/card.json',
      query_id: 7,
      status: 200,
      body: {
        imt_id: 999,
        nm_id: 111,
        imt_name: 'Кроссовки беговые мужские',
        slug: 'krossovki-begovye-muzhskie',
        vendor_code: '22123456',
        subj_name: 'Кроссовки',
        subj_root_name: 'Обувь',
        description: 'Беговые кроссовки',
        markdown_description: 'Беговые кроссовки **мужские**',
        need_kiz: true,
        create_date: '2022-03-01T00:00:00Z',
        update_date: '2026-06-01T00:00:00Z',
        options: [
          { name: 'Состав', value: 'текстиль 100%', charc_type: 1 },
          { name: 'Цвет', value: 'черный', charc_type: 1, is_variable: true, variable_values: ['черный', 'белый'] },
          { name: 'Вид спорта', value: 'бег', charc_type: 1 },
        ],
        compositions: [{ name: 'текстиль 100%' }],
        sizes_table: {
          details_props: ['RU', 'Длина стельки, см'],
          values: [
            { tech_size: '39', chrt_id: 134002594, details: ['39', '25'] },
            { tech_size: '40', chrt_id: 134002595, details: ['40', ''] }, // sparse: empty cell skipped
          ],
        },
        colors: [111, 222333],
        nm_colors_names: 'черный',
        contents: 'Кроссовки 1 шт',
        kinds: ['Мужчины'],
        has_seller_recommendations: true,
        user_flags: 1,
        selling: { brand_name: 'Nike', brand_hash: 'ABC123', supplier_id: 900 },
        media: { photo_count: 6, has_video: true },
        data: { subject_id: 81, subject_root_id: 4, chrt_ids: [134002594, 134002595] },
        grouped_options: [
          { group_name: 'Основная информация', options: [{ name: 'Состав', value: 'текстиль 100%', charc_type: 1 }, { name: 'Цвет', value: 'черный', charc_type: 1 }] },
          { group_name: 'Дополнительная информация', options: [{ name: 'Вид спорта', value: 'бег', charc_type: 1 }] },
        ],
      },
    },
  ];
}
