// src/db/upsert.ts — stable query_id resolution for the search_queries dimension.
// Port of UpsertQuery semantics in pkg/wbscraper/types.go + writer adapters: a given query text
// always resolves to one query_id across sessions (the &query UNIQUE index is the anchor).
//
// The check-then-add happens inside a single rw transaction so two concurrent upserts of the same
// text cannot create two rows (the UNIQUE constraint would also reject the second, but the tx keeps
// it deterministic and returns the existing id rather than throwing).

import type { Table } from 'dexie';
import { db } from './dexie';
import type { SearchQuery } from './types';

/** Resolve one query to its stable query_id, inserting it if new. Returns the id. */
export async function upsertQuery(q: SearchQuery): Promise<number> {
  return db.transaction('rw', db.search_queries, async () => {
    const existing = await db.search_queries.where('query').equals(q.query).first();
    if (existing?.query_id != null) return existing.query_id;
    return db.search_queries.add(stripId(q));
  });
}

/** Bulk-resolve many queries in one transaction. Returns a map query-text → query_id (stable across
 *  sessions). Dedups within the batch and against existing rows. Used by the constructor (S4). */
export async function upsertQueries(qs: SearchQuery[]): Promise<Map<string, number>> {
  const out = new Map<string, number>();
  if (qs.length === 0) return out;
  await db.transaction('rw', db.search_queries, async () => {
    for (const q of qs) {
      if (out.has(q.query)) continue; // already resolved this batch
      const existing = await db.search_queries.where('query').equals(q.query).first();
      const id = existing?.query_id != null ? existing.query_id : await db.search_queries.add(stripId(q));
      out.set(q.query, id);
    }
  });
  return out;
}

/** stripId returns a copy without query_id so Dexie assigns the next autoinc (never reuse an id). */
function stripId(q: SearchQuery): Omit<SearchQuery, 'query_id'> {
  const { query_id: _omit, ...rest } = q;
  void _omit;
  return rest;
}

// Re-export the table for tests/tools that want to clear it.
export const queriesTable: Table<SearchQuery, number> = db.search_queries;
