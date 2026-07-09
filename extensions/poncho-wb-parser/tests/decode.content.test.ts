// tests/decode.content.test.ts — decodeCardContent: the flat wbbasket.ru card.json CDN object →
// 1 meta row + N option rows. Pure unit test — no chrome, no DB.

import { describe, it, expect } from 'vitest';
import { decodeCardContent } from '../src/decode/content';
import type { Intercept } from '../src/db/types';

const S = '2026-07-08T12:00:00Z';

function intercept(body: unknown, url = 'https://basket-10.wbbasket.ru/vol1/part111/111/info/ru/card.json'): Intercept {
  return { kind: 'card_content', url, query_id: 7, status: 200, body };
}

describe('decodeCardContent', () => {
  it('turns a flat CDN object into 1 meta + N options (+ full card.json: materials/sizes/colors)', () => {
    const out = decodeCardContent(
      intercept({
        imt_id: 999,
        nm_id: 111,
        imt_name: 'Кроссовки беговые мужские',
        slug: 'krossovki-begovye',
        vendor_code: '22123456',
        subj_name: 'Кроссовки',
        subj_root_name: 'Обувь',
        description: 'Беговые',
        markdown_description: 'Беговые **мужские**',
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
            { tech_size: '40', chrt_id: 134002595, details: ['40', ''] }, // empty cell skipped
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
      }),
      S,
    );

    expect(out.competitor_card_meta).toHaveLength(1);
    const m = out.competitor_card_meta[0]!;
    expect(m.nm_id).toBe(111);
    expect(m.vendor_code).toBe('22123456');
    expect(m.subj_name).toBe('Кроссовки');
    expect(m.subj_root_name).toBe('Обувь');
    expect(m.need_kiz).toBe(1);
    expect(m.description).toBe('Беговые **мужские**'); // markdown_description preferred over plain
    expect(m.create_date).toBe('2022-03-01T00:00:00Z');
    // Этап A — расширенные скаляры:
    expect(m.imt_id).toBe(999);
    expect(m.imt_name).toBe('Кроссовки беговые мужские');
    expect(m.slug).toBe('krossovki-begovye');
    expect(m.brand_name).toBe('Nike');
    expect(m.brand_hash).toBe('ABC123');
    expect(m.supplier_id).toBe(900);
    expect(m.photo_count).toBe(6);
    expect(m.has_video).toBe(1);
    expect(m.subject_id).toBe(81);
    expect(m.subject_root_id).toBe(4);
    expect(m.nm_colors_names).toBe('черный');
    expect(m.contents).toBe('Кроссовки 1 шт');
    expect(m.has_seller_recommendations).toBe(1);
    expect(m.user_flags).toBe(1);
    expect(m.kinds).toBe('["Мужчины"]');

    expect(out.competitor_card_options).toHaveLength(3);
    const color = out.competitor_card_options.find((o) => o.char_name === 'Цвет')!;
    expect(color.is_variable).toBe(1);
    expect(color.variable_values).toBe('["черный","белый"]'); // JSON-serialized array
    expect(color.group_name).toBe('Основная информация'); // resolved from grouped_options[]
    const sostav = out.competitor_card_options.find((o) => o.char_name === 'Состав')!;
    expect(sostav.is_variable).toBe(0);
    expect(sostav.variable_values).toBe(''); // no variable_values → ''
    expect(sostav.group_name).toBe('Основная информация');
    expect(out.competitor_card_options.find((o) => o.char_name === 'Вид спорта')!.group_name).toBe('Дополнительная информация');

    // compositions (материалы)
    expect(out.competitor_card_compositions).toHaveLength(1);
    expect(out.competitor_card_compositions[0]!.name).toBe('текстиль 100%');
    expect(out.competitor_card_compositions[0]!.ord).toBe(0);

    // sizes grid: (39,RU=39),(39,Длина=25),(40,RU=40) — the (40,Длина='') empty cell is dropped
    expect(out.competitor_card_sizes).toHaveLength(3);
    const s39dl = out.competitor_card_sizes.find((s) => s.tech_size === '39' && s.prop_name === 'Длина стельки, см')!;
    expect(s39dl.prop_value).toBe('25');
    expect(s39dl.chrt_id).toBe(134002594);
    expect(s39dl.prop_order).toBe(1);
    expect(out.competitor_card_sizes.some((s) => s.tech_size === '40' && s.prop_name === 'Длина стельки, см')).toBe(false); // empty cell skipped

    // colors (color-variant nm_ids)
    expect(out.competitor_card_colors).toHaveLength(2);
    expect(out.competitor_card_colors.map((c) => c.color_nm_id)).toEqual([111, 222333]);
    expect(out.competitor_card_colors[0]!.ord).toBe(0);
  });

  it('coerces need_kiz false → 0, defaults missing description → ""', () => {
    const out = decodeCardContent(intercept({ nm_id: 222, need_kiz: false }), S);
    const m = out.competitor_card_meta[0]!;
    expect(m.need_kiz).toBe(0);
    expect(m.description).toBe('');
    expect(m.vendor_code).toBe('');
    // Этап A defaults: nullable numerics → null, text → '', flags → 0
    expect(m.imt_id).toBeNull();
    expect(m.imt_name).toBe('');
    expect(m.brand_name).toBe('');
    expect(m.supplier_id).toBeNull();
    expect(m.photo_count).toBe(0);
    expect(m.has_video).toBe(0);
    expect(m.subject_id).toBeNull();
    expect(m.kinds).toBe('');
    expect(out.competitor_card_options).toHaveLength(0);
    expect(out.competitor_card_compositions).toHaveLength(0);
    expect(out.competitor_card_sizes).toHaveLength(0);
    expect(out.competitor_card_colors).toHaveLength(0);
  });

  it('falls back to nm_id parsed from the URL when the body omits it', () => {
    const out = decodeCardContent(
      intercept({ vendor_code: 'X' }, 'https://basket-3.wbbasket.ru/vol4/part445378/445378637/info/ru/card.json'),
      S,
    );
    expect(out.competitor_card_meta[0]!.nm_id).toBe(445378637);
  });

  it('emits nothing when neither body nor URL yields an nm_id', () => {
    const out = decodeCardContent(intercept({ vendor_code: 'X' }, 'https://basket-3.wbbasket.ru/foo/bar.json'), S);
    expect(out.competitor_card_meta).toHaveLength(0);
    expect(out.competitor_card_options).toHaveLength(0);
    expect(out.competitor_card_compositions).toHaveLength(0);
    expect(out.competitor_card_sizes).toHaveLength(0);
    expect(out.competitor_card_colors).toHaveLength(0);
  });

  it('throws on a non-object body (malformed capture)', () => {
    expect(() => decodeCardContent(intercept(null), S)).toThrow();
    expect(() => decodeCardContent(intercept([1, 2, 3]), S)).toThrow();
  });
});
