// src/offscreen/harvest.ts — pure (chrome/DB-free) helpers for the detail-harvest.
//
// Extracted from orchestrator.ts so the polling logic is unit-testable without importing the
// orchestrator (whose top-level registers a chrome.runtime.onMessage listener — untestable under
// the partial chrome mock in tests/setup.ts).

/**
 * Poll `read()` until it returns a non-empty list, hitting `maxPolls` attempts at most (or
 * short-circuiting when `shouldStop()` turns true). `snooze` is awaited between attempts (but not
 * after the last one, and not after a success).
 *
 * Why this exists: the detail-harvest reads `search_positions` from Dexie, but those rows are
 * written ASYNCHRONOUSLY by the capture pipeline (inject → CAPTURE → decodeAndPersist). When a
 * search returns few results the scroll loop breaks early (WB `grew=false`) and the harvest runs
 * ~2s after navigate — before the ~3s-slow /search capture has landed. A one-shot read returns []
 * → no /detail navigation → no per-wh stocks/details captured. Polling waits for the rows to
 * appear instead, and times out (returns []) if the search genuinely delivered nothing.
 */
export async function pollUntilNonEmpty<T>(
  read: () => Promise<readonly T[]>,
  shouldStop: () => boolean,
  snooze: (ms: number) => Promise<void>,
  pollMs: number,
  maxPolls: number,
): Promise<readonly T[]> {
  let rows: readonly T[] = [];
  for (let i = 0; i < maxPolls; i++) {
    rows = await read();
    if (rows.length > 0) return rows;
    if (shouldStop()) return rows;
    if (i < maxPolls - 1) await snooze(pollMs); // no snooze after the final attempt
  }
  return rows;
}

/**
 * Pick up to `limit` nm_ids from `rows` that aren't already in `harvested`, recording each pick
 * into `harvested` (so a later harvest on the same session doesn't re-open the same card).
 */
export function pickUnharvested(rows: readonly { nm_id: number }[], harvested: Set<number>, limit: number): number[] {
  const out: number[] = [];
  for (const r of rows) {
    if (harvested.has(r.nm_id)) continue;
    harvested.add(r.nm_id);
    out.push(r.nm_id);
    if (out.length >= limit) break;
  }
  return out;
}
