// src/export/push.ts — ship a finished snapshot to the Go collector (POST /snapshot).
//
// The extension stays browser-only when no server URL is configured: pushSnapshot is a no-op
// (shipped:false) then, so all prior behaviour (Dexie-only, JSON download button) is unchanged.
// Once a URL is set, every finished snapshot is dumped (all 11 fact tables, see json-dump.ts) and
// POSTed as already-decoded rows — the server re-resolves query_id and persists atomically.
//
// Resilience: a snapshot that finishes collecting but cannot ship (server down, network error,
// no server URL configured yet, SW death mid-push) is held in a persistent `pending_shipments`
// queue and retried on the next COLLECT_DONE and on SW start. The server's replace-by-snapshot
// makes retries idempotent, so a snapshot that shipped-then-was-retried does not duplicate
// (DELETE WHERE snapshot_ts first).
//
// A browser-only skip (no URL) is a DEFERRAL, not a resolution: such a snapshot STAYS queued so
// the user can set a URL later and push retrospectively (the "Отправить сейчас" button exists for
// exactly this). shipPending short-circuits on the missing URL (no fetch, no drain), so retaining
// is zero-cost — shipPending is event-driven, never a busy loop.

import { dumpSnapshot } from './json-dump';
import { loadServerUrl } from '../storage/config';

const KEY_PENDING = 'pending_shipments';

/** Outcome of one push attempt. `shipped:false` + `ok:true` = browser-only skip (no error). */
export interface PushResult {
  ok: boolean; // false only on a real failure (network/HTTP) — a skip is ok:true
  shipped: boolean; // true iff a POST reached the server and it returned 2xx
  snapshot: string;
  counts?: Record<string, number>; // the server's per-table insert counts (when shipped)
  error?: string;
}

/** Push one snapshot to the configured Go server. No-op (shipped:false, ok:true) when no URL is
 *  set — browser-only mode is the default, not an error. The endpoint is ${url}/snapshot. */
export async function pushSnapshot(snapshot: string): Promise<PushResult> {
  const url = await loadServerUrl();
  if (!url) return { ok: true, shipped: false, snapshot };

  const dump = await dumpSnapshot(snapshot);
  const endpoint = url.replace(/\/+$/, '') + '/snapshot';
  let resp: Response;
  try {
    resp = await fetch(endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(dump),
    });
  } catch (e) {
    return { ok: false, shipped: false, snapshot, error: `network: ${String(e)}` };
  }
  if (!resp.ok) {
    return { ok: false, shipped: false, snapshot, error: `HTTP ${resp.status} ${resp.statusText}`.trim() };
  }
  const body = (await resp.json().catch(() => ({}))) as { counts?: Record<string, number>; snapshot?: string };
  return { ok: true, shipped: true, snapshot, counts: body.counts };
}

/** Add a snapshot to the pending-shipment queue (deduped). Called on COLLECT_DONE so a snapshot
 *  that finished is never lost even if the push is deferred. Persists immediately. */
export async function enqueueShipment(snapshot: string): Promise<void> {
  if (!snapshot) return;
  const list = await loadPending();
  if (list.includes(snapshot)) return;
  await chrome.storage.local.set({ [KEY_PENDING]: [...list, snapshot] }).catch(() => {});
}

/** Try to ship every pending snapshot. A snapshot leaves the queue ONLY when it actually ships
 *  (`shipped:true`); a failure (ok:false) is retried next time, and a browser-only skip (no URL) is
 *  a deferral that STAYS queued for retroactive push once a URL is set. Returns a per-snapshot
 *  summary so the caller can log/broadcast the outcome. */
export async function shipPending(): Promise<PushResult[]> {
  const list = await loadPending();
  if (list.length === 0) return [];
  // No server configured: nothing can ship, and we must NOT drain — the snapshot should push once a
  // URL is set later. Short-circuit with one skip result per snapshot, leaving storage untouched and
  // firing zero fetches (the zero-cost answer to the old "re-attempt forever" worry: shipPending is
  // event-driven, so retaining queued ts strings is harmless).
  const url = await loadServerUrl();
  if (!url) return list.map((snapshot) => ({ ok: true, shipped: false, snapshot }));
  const results: PushResult[] = [];
  const stillPending: string[] = [];
  for (const snap of list) {
    const r = await pushSnapshot(snap);
    results.push(r);
    // Keep unless it actually shipped. A skip is impossible here (URL is set), so this only retains
    // real failures (network/HTTP) for the next COLLECT_DONE / SW-start retry.
    if (!r.shipped) stillPending.push(snap);
  }
  await chrome.storage.local.set({ [KEY_PENDING]: stillPending }).catch(() => {});
  return results;
}

/** Read the pending-shipment queue (snapshots that finished but have not shipped). */
export async function loadPending(): Promise<string[]> {
  const s = await chrome.storage.local.get(KEY_PENDING).catch(() => ({}) as Record<string, unknown>);
  const v = s[KEY_PENDING];
  return Array.isArray(v) ? (v as string[]) : [];
}
