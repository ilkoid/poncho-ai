// tests/querygen.test.ts — port of the cartesian-product tests in pkg/wbscraper/querygen_test.go.
// Tests the pure `cartesian` function (no chrome, no DB) — the generator is DB-free; query_id is
// assigned later by the upsert layer (tested in upsert.test.ts).

import { describe, it, expect } from 'vitest';
import { cartesian, type ConstructorConfig } from '../src/querygen/static';

const base: ConstructorConfig = { subjects: [], brand: [], gender: [], season: [], age: [], material: [], purpose: [], comment: '', max_queries: 0, dedup: true };

describe('cartesian', () => {
  it('nested product with "" skip token across dimensions', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['кроссовки'],
      gender: ['для мальчика', ''],
      season: ['зима', ''],
      age: [''],
    });
    expect(seeds.map((s) => s.query)).toEqual([
      'кроссовки для мальчика зима',
      'кроссовки для мальчика',
      'кроссовки зима',
      'кроссовки',
    ]);
  });

  it('dedup=true collapses same-text queries (different provenance) keeping the first', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['кроссовки', 'кроссовки детские'],
      age: ['', 'детские'],
      dedup: true,
    });
    // 4 raw cells, 1 collision ("кроссовки детские") → 3 distinct; first provenance kept.
    expect(seeds).toHaveLength(3);
    const kd = seeds.find((s) => s.query === 'кроссовки детские');
    expect(kd).toBeDefined();
    expect(`${kd!.subject}/${kd!.age}`).toBe('кроссовки/детские'); // first occurrence wins
  });

  it('dedup=false keeps all cells', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['кроссовки', 'кроссовки детские'],
      age: ['', 'детские'],
      dedup: false,
    });
    expect(seeds.length).toBe(4);
  });

  it('max_queries caps the post-dedup list (<=0 = unlimited)', () => {
    const capped = cartesian({ ...base, subjects: ['a', 'b', 'c', 'd'], max_queries: 2 });
    expect(capped.map((s) => s.query)).toEqual(['a', 'b']);
    const unlimited = cartesian({ ...base, subjects: ['a', 'b', 'c', 'd'], max_queries: 0 });
    expect(unlimited.length).toBe(4);
  });

  it('unconfigured dimensions collapse to subject-only queries', () => {
    const seeds = cartesian({ ...base, subjects: ['бейсболки', 'рюкзаки'] });
    expect(seeds.map((s) => s.query)).toEqual(['бейсболки', 'рюкзаки']);
  });

  it('empty subjects yields no seeds (degenerate config)', () => {
    expect(cartesian({ ...base, subjects: [] })).toHaveLength(0);
  });

  it('material × purpose are cartesian axes (multiplied; token order … material purpose)', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['куртка'],
      material: ['текстиль', 'кожа'],
      purpose: ['для походов', 'для города'],
    });
    expect(seeds.map((s) => s.query)).toEqual([
      'куртка текстиль для походов',
      'куртка текстиль для города',
      'куртка кожа для походов',
      'куртка кожа для города',
    ]);
  });

  it('comment is appended to every query but does NOT multiply the product', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['футболка'],
      material: ['текстиль', 'кожа'],
      comment: 'недорогие',
    });
    // 1 subject × 2 material = 2 (comment is per-cell, never looped)
    expect(seeds).toHaveLength(2);
    expect(seeds.map((s) => s.query)).toEqual(['футболка текстиль недорогие', 'футболка кожа недорогие']);
    expect(seeds.every((s) => s.comment === 'недорогие')).toBe(true);
  });

  it('empty comment leaves no trailing space', () => {
    const seeds = cartesian({ ...base, subjects: ['кроссовки'], comment: '' });
    expect(seeds).toHaveLength(1);
    expect(seeds[0]!.query).toBe('кроссовки'); // no trailing space
  });

  it('comment is part of the dedup key (appended before dedup)', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['кроссовки', 'кроссовки'], // duplicate subject
      comment: 'летние',
    });
    expect(seeds).toHaveLength(1); // two identical "кроссовки летние" collapse
    expect(seeds[0]!.query).toBe('кроссовки летние');
  });

  it('brand is a cartesian axis threaded right after subject (subject brand …)', () => {
    const seeds = cartesian({
      ...base,
      subjects: ['кроссовки'],
      brand: ['Nike', 'Adidas'],
      material: ['текстиль'],
    });
    // brand sits right after subject in joinTokens: "subject brand …"
    expect(seeds.map((s) => s.query)).toEqual(['кроссовки Nike текстиль', 'кроссовки Adidas текстиль']);
    expect(seeds[0]!.brand).toBe('Nike');
    expect(seeds[1]!.brand).toBe('Adidas');
  });

  it('empty brand list behaves like an unconfigured dimension (no token added)', () => {
    const seeds = cartesian({ ...base, subjects: ['кроссовки'], brand: [] });
    expect(seeds).toHaveLength(1);
    expect(seeds[0]!.query).toBe('кроссовки'); // brand contributes nothing
    expect(seeds[0]!.brand).toBe(''); // dim(['']) yields the '' token
  });
});
