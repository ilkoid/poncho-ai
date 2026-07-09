// tests/push.test.ts — snapshot shipment to the Go collector (POST /snapshot).
// Covers the push.ts contract: browser-only skip, success path, HTTP/network errors, and the
// pending-queue lifecycle (dedup on enqueue, success-removal / failure-retention on shipPending).
// fetch is stubbed via vi.stubGlobal; chrome.storage.local is the in-memory mock from setup.ts.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { pushSnapshot, enqueueShipment, shipPending, loadPending } from '../src/export/push';
import { saveServerUrl } from '../src/storage/config';
import { resetMockStorage } from './setup';

const SNAP = '2026-07-08T10:00:00Z';

/** A minimal fetch stub returning a 200 with the server's per-table counts shape. */
function fetchOk(counts: Record<string, number> = { cards: 1, meta: 1 }): void {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => ({ snapshot: SNAP, counts }),
  })) as unknown as typeof fetch);
}

function fetchHttpError(status: number): void {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: false,
    status,
    statusText: 'Forbidden',
    json: async () => ({}),
  })) as unknown as typeof fetch);
}

beforeEach(() => {
  resetMockStorage();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('pushSnapshot', () => {
  it('no server URL → browser-only skip (shipped:false, ok:true, no fetch)', async () => {
    // server_url unset → pushSnapshot must NOT attempt any network call. Prove it by stubbing
    // fetch to throw: if pushSnapshot reached it, the call would reject, not skip.
    vi.stubGlobal('fetch', vi.fn(() => {
      throw new Error('fetch must not be called in browser-only mode');
    }) as unknown as typeof fetch);
    const r = await pushSnapshot(SNAP);
    expect(r.ok).toBe(true);
    expect(r.shipped).toBe(false);
  });

  it('configured URL → POST ${url}/snapshot, returns the server counts', async () => {
    fetchOk({ positions: 1, cards: 2, meta: 1, compositions: 1 });
    await saveServerUrl('http://127.0.0.1:7780/');
    const r = await pushSnapshot(SNAP);
    expect(r.ok).toBe(true);
    expect(r.shipped).toBe(true);
    expect(r.counts).toEqual({ positions: 1, cards: 2, meta: 1, compositions: 1 });
    const f = fetch as unknown as ReturnType<typeof vi.fn>;
    expect(f).toHaveBeenCalledTimes(1);
    const [url, init] = f.mock.calls[0]!;
    expect(url).toBe('http://127.0.0.1:7780/snapshot'); // trailing slash stripped, /snapshot appended
    expect((init as RequestInit).method).toBe('POST');
    expect(typeof (init as RequestInit).body).toBe('string'); // the serialized SnapshotDump
  });

  it('HTTP non-2xx → ok:false, shipped:false, error carries the status', async () => {
    fetchHttpError(403);
    await saveServerUrl('http://localhost:7780');
    const r = await pushSnapshot(SNAP);
    expect(r.ok).toBe(false);
    expect(r.shipped).toBe(false);
    expect(r.error).toContain('403');
  });

  it('network throw → ok:false with a network: error', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new Error('ECONNREFUSED');
    }) as unknown as typeof fetch);
    await saveServerUrl('http://localhost:7780');
    const r = await pushSnapshot(SNAP);
    expect(r.ok).toBe(false);
    expect(r.shipped).toBe(false);
    expect(r.error).toContain('network');
  });
});

describe('pending queue', () => {
  it('enqueueShipment dedups the same snapshot', async () => {
    await enqueueShipment(SNAP);
    await enqueueShipment(SNAP);
    expect(await loadPending()).toEqual([SNAP]);
  });

  it('shipPending removes successes and returns their results', async () => {
    fetchOk();
    await saveServerUrl('http://localhost:7780');
    await enqueueShipment('snap-a');
    await enqueueShipment('snap-b');
    const results = await shipPending();
    expect(results).toHaveLength(2);
    expect(results.every((r) => r.shipped)).toBe(true);
    expect(await loadPending()).toEqual([]); // both shipped → queue drained
  });

  it('shipPending RETAINS failures for the next retry (server down)', async () => {
    fetchHttpError(500);
    await saveServerUrl('http://localhost:7780');
    await enqueueShipment('snap-fail');
    const results = await shipPending();
    expect(results[0]!.ok).toBe(false);
    expect(await loadPending()).toEqual(['snap-fail']); // stayed queued
  });

  it('shipPending RETAINS browser-only skips (deferred, not resolved — retroactive push after URL set)', async () => {
    // No server URL configured → the snapshot cannot ship yet, but it must STAY queued so it pushes
    // once a URL is set later. A skip is a deferral, not a resolution. shipPending short-circuits on
    // the missing URL (no fetch, no drain), so retaining queued snapshots is zero-cost.
    await enqueueShipment('snap-skip');
    const results = await shipPending();
    expect(results[0]!.ok).toBe(true);
    expect(results[0]!.shipped).toBe(false);
    expect(await loadPending()).toEqual(['snap-skip']); // stayed queued for retroactive push
  });

  it('shipPending with no URL short-circuits without calling fetch (queue intact, multiple snapshots)', async () => {
    // Prove no network work happens when there is no server to reach: stub fetch to throw, enqueue
    // two snapshots, and confirm both are returned as skips and retained untouched.
    vi.stubGlobal('fetch', vi.fn(() => {
      throw new Error('fetch must not be called when no server URL is configured');
    }) as unknown as typeof fetch);
    await enqueueShipment('snap-a');
    await enqueueShipment('snap-b');
    const results = await shipPending();
    expect(results).toHaveLength(2);
    expect(results.every((r) => r.ok && !r.shipped)).toBe(true);
    expect(fetch).not.toHaveBeenCalled();
    expect(await loadPending()).toEqual(['snap-a', 'snap-b']); // both retained
  });

  it('shipPending is a no-op on an empty queue (no fetch)', async () => {
    fetchOk();
    const results = await shipPending();
    expect(results).toEqual([]);
    expect(fetch).not.toHaveBeenCalled();
  });
});
