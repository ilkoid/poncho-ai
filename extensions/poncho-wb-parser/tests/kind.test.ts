// tests/kind.test.ts — pins the COLLECT_PATTERNS classifier against real WB storefront URL shapes.
// If WB bumps a version (v4→v5, v18→v19) the /v\d+/ guards still match; if they restructure a
// path, a capture starts going to null (skipped) and this test surfaces it.

import { describe, it, expect } from 'vitest';
import { classify } from '../src/decode/kind';

describe('classify — URL → capture kind', () => {
  it('card_detail: /__internal/card/cards/vN/detail', () => {
    expect(classify('https://www.wildberries.ru/__internal/card/cards/v4/detail?app=1')).toBe('card_detail');
  });
  it('card_list: /__internal/card/cards/vN/list', () => {
    expect(classify('https://www.wildberries.ru/__internal/card/cards/v5/list?curr=rub')).toBe('card_list');
  });
  it('search: /__internal/search/exactmatch/<seg>/common/vN/search', () => {
    expect(
      classify('https://www.wildberries.ru/__internal/search/exactmatch/byabstractly/common/v18/search?ab_testing=false'),
    ).toBe('search');
  });
  it('ad: __internal/banners/shelfs/search', () => {
    expect(classify('https://www.wildberries.ru/__internal/banners/shelfs/search?query=x')).toBe('ad');
  });
  it('ad: banners-website v2/banners', () => {
    expect(classify('https://banners-website.wildberries.ru/public/v2/banners?urltype=1024')).toBe('ad');
  });
  it('brand: /__internal/catalog/brands/vN/catalog', () => {
    expect(classify('https://www.wildberries.ru/__internal/catalog/brands/v1/catalog?brand=123')).toBe('brand');
  });
  it('brand: /__internal/catalog/brands/vN/filters', () => {
    expect(classify('https://www.wildberries.ru/__internal/catalog/brands/v2/filters?brand=123')).toBe('brand');
  });
  it('unrelated WB endpoint → null (skipped, not collected)', () => {
    expect(classify('https://www.wildberries.ru/webapi/spa/promotions/metatags')).toBeNull();
    expect(classify('https://www.wildberries.ru/')).toBeNull();
  });
});
