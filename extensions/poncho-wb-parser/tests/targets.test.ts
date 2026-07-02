// tests/targets.test.ts — buildTargets constructor branch e2e: saved constructor → cartesian →
// upsert → Target[] with STABLE query_ids across two builds (the cross-session join invariant).
// Uses the chrome.storage.local mock from setup.ts.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { buildTargets } from '../src/querygen/targets';
import { saveConstructor } from '../src/storage/config';
import { resetMockStorage } from './setup';

beforeEach(async () => {
  resetMockStorage();
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

describe('buildTargets — constructor branch', () => {
  it('resolves stable query_ids across two builds and builds search targets', async () => {
    await saveConstructor({
      subjects: ['кроссовки', 'ботинки'],
      gender: [],
      season: [],
      age: [],
      material: ['текстиль'],
      purpose: ['для бега'],
      comment: 'недорогие',
      max_queries: 0,
      dedup: true,
    });

    const a = await buildTargets({ source: 'constructor' });
    const b = await buildTargets({ source: 'constructor' });

    // comment appended; material/purpose threaded into the query string
    expect(a.map((t) => t.query)).toEqual(['кроссовки текстиль для бега недорогие', 'ботинки текстиль для бега недорогие']);
    expect(a[0]!.material).toBe('текстиль');
    expect(a[0]!.purpose).toBe('для бега');
    expect(a[0]!.comment).toBe('недорогие');
    // query text → one stable id across builds (the cross-session join key)
    expect(a[0]!.query_id).toBe(b[0]!.query_id);
    expect(a[1]!.query_id).toBe(b[1]!.query_id);
    expect(a[0]!.query_id).not.toBe(a[1]!.query_id);
    // well-formed search targets
    expect(a.every((t) => t.kind === 'search' && t.query_id != null && t.url.includes('/catalog/0/search.aspx?search='))).toBe(true);
  });
});
