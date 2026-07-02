// src/querygen/targets.ts — build Target[] from a CollectSource, resolving stable query_ids via
// upsert. Port of the target-construction half of querygen.go + the SW's COLLECT_START prep.
//
// S3 scope: single query / single nmId (debug + recon). S4 expands the 'constructor' branch into
// the full cartesian subject×gender×season×age (port of querygen.go).

import type { Target } from '../messages';
import type { CollectSource } from '../messages';
import { upsertQuery, upsertQueries } from '../db/upsert';
import { cartesian } from './static';
import { loadConstructor } from '../storage/config';

/** WB search URL for a query text (matches v1: /catalog/0/search.aspx?search=). */
export const searchUrl = (q: string): string =>
  `https://www.wildberries.ru/catalog/0/search.aspx?search=${encodeURIComponent(q)}`;

/** WB card detail URL for an nmId. */
export const detailUrl = (nmId: number): string => `https://www.wildberries.ru/catalog/${nmId}/detail.aspx`;

/** Build the target list for a session, resolving stable query_ids for search targets. */
export async function buildTargets(collect: CollectSource): Promise<Target[]> {
  if (collect.source === 'single') {
    if (collect.singleQuery && collect.singleQuery.trim() !== '') {
      const query = collect.singleQuery.trim();
      const query_id = await upsertQuery({ query, subject: '', gender: '', season: '', age: '', material: '', purpose: '', comment: '' });
      return [{ kind: 'search', query_id, query, url: searchUrl(query), subject: '', gender: '', season: '', age: '', material: '', purpose: '', comment: '' }];
    }
    if (collect.singleNmId != null) {
      return [{ kind: 'card', query_id: null, query: '', url: detailUrl(collect.singleNmId), subject: '', gender: '', season: '', age: '', material: '', purpose: '', comment: '' }];
    }
    return [];
  }
  // constructor source: cartesian product of saved lists → upsert each text → Target[].
  // query_id is stable across sessions (UNIQUE on query text), so a re-run reuses existing ids.
  const config = await loadConstructor();
  const seeds = cartesian(config);
  if (seeds.length === 0) return [];
  const idMap = await upsertQueries(seeds);
  return seeds.map((s) => ({
    kind: 'search' as const,
    query_id: idMap.get(s.query) ?? null,
    query: s.query,
    url: searchUrl(s.query),
    subject: s.subject,
    gender: s.gender,
    season: s.season,
    age: s.age,
    material: s.material,
    purpose: s.purpose,
    comment: s.comment,
  }));
}
