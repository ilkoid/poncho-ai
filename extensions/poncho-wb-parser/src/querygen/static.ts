// src/querygen/static.ts — the cartesian constructor. Pure port of StaticGenerator.Generate
// in pkg/wbscraper/querygen.go (no chrome, no DB → unit-testable in isolation).
//
// Construction rules (mirror the Go source, extended with material/purpose/comment):
//   - subjects is the seed list and the outer axis; every query contains a subject.
//   - the "" token in any list dimension means "skip that dimension for this cell"; non-empty
//     tokens are joined with a single space into the query text.
//   - `comment` is a SINGLE free-text string (not a list); it is appended to EVERY query and is
//     NOT multiplied into the product (one value → same cartesian count).
//   - duplicate query strings are dropped when dedup is true (first tuple in iteration order
//     wins; dedup is by query text, not attribute tuple).
//   - max_queries caps the post-dedup list (<=0 = unlimited) as an explosion guard.

export interface ConstructorConfig {
  subjects: string[];
  gender: string[];
  season: string[];
  age: string[];
  material: string[]; // cartesian axis (текстиль, кожа, …); "" line = skip
  purpose: string[]; // cartesian axis (для походов, для офиса, …); "" line = skip
  comment: string; // single free-text appended to EVERY query, NOT multiplied
  max_queries: number; // <= 0 = unlimited
  dedup: boolean;
}

/** One constructed query (pre-upsert; query_id is assigned by the DB layer). */
export interface ConstructorSeed {
  query: string;
  subject: string;
  gender: string;
  season: string;
  age: string;
  material: string;
  purpose: string;
  comment: string;
}

/** Default constructor — mirrors cmd/.configs/download-all/wb-scraper-collector.yaml. */
export const DEFAULT_CONSTRUCTOR: ConstructorConfig = {
  subjects: ['бейсболки'],
  gender: ['для девочки'],
  season: ['летние'],
  age: [''],
  material: [],
  purpose: [],
  comment: '',
  max_queries: 300,
  dedup: true,
};

/** cartesian walks subjects × gender × season × age × material × purpose (deterministic order),
 *  appends `comment` verbatim to every cell, and returns one seed per surviving, deduplicated
 *  query. An empty subject list yields an empty array. `comment` is applied per-cell, never looped,
 *  so it does not grow the product. */
export function cartesian(c: ConstructorConfig): ConstructorSeed[] {
  const seen = new Set<string>();
  const out: ConstructorSeed[] = [];
  for (const subject of dim(c.subjects)) {
    for (const gender of dim(c.gender)) {
      for (const season of dim(c.season)) {
        for (const age of dim(c.age)) {
          for (const material of dim(c.material)) {
            for (const purpose of dim(c.purpose)) {
              const query = joinTokens(subject, gender, season, age, material, purpose, c.comment);
              if (query === '') continue; // all dimensions empty for this cell — nothing to search
              if (c.dedup) {
                if (seen.has(query)) continue;
                seen.add(query);
              }
              out.push({ query, subject, gender, season, age, material, purpose, comment: c.comment });
            }
          }
        }
      }
    }
  }
  if (c.max_queries > 0 && out.length > c.max_queries) out.length = c.max_queries;
  return out;
}

/** dimension returns the list unchanged, or [''] for an empty list so the product still iterates
 *  once over that axis (the "" contributes nothing). Without this, an unconfigured dimension
 *  collapses the whole product to zero. */
function dim(d: string[]): string[] {
  return d.length === 0 ? [''] : d;
}

/** joinTokens joins non-empty parts with single spaces, preserving order ("" = skip token). */
export function joinTokens(...parts: string[]): string {
  return parts.filter((p) => p !== '').join(' ');
}

/** Parse a textarea into a token list: one token per non-empty line, trimmed. */
export function parseTextarea(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l !== '');
}
