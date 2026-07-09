// src/offscreen/orchestrator.ts — owns the collect run-loop + Dexie writes.
// Port of extensions/wb-scraper/src/offscreen.js, rewritten LOCAL (no Go collector):
//   - COLLECT_LOOP drives the per-target sequence (navigate → scroll pages → detail-harvest),
//     paced at human speed (1-3s) so the WB SPA fires its /search + /detail requests.
//   - CAPTURE (forwarded by the SW from MAIN-world intercepts) is decoded + persisted to Dexie
//     immediately — no HTTP batching, since there is no collector.
//   - MOCK_DECODE is the no-browser test hook: decode + persist synthetic intercepts, proving the
//     write path (and SW-death resilience — writes live HERE, not in the volatile SW).
//
// Timing knobs match v1: MAX_PAGES=3, DETAIL_K=8, 1-3s pauses.

import { db } from '../db/dexie';
import type { Decoded, Intercept, SnapshotTs } from '../db/types';
import { Decode } from '../decode';
import { persistDecoded, dedupeBySeen, freshSeen } from '../db/write';
import type { FactCounts, FromOffscreen, Target, ToOffscreen } from '../messages';
import { EMPTY_COUNTS } from '../messages';
import { detailUrl } from '../querygen/targets';
import { pollUntilNonEmpty, pickUnharvested } from './harvest';
import { dropCascadeCards } from './filter';
import { DEFAULT_DETAIL_K } from '../storage/config';

const MAX_PAGES = 3;
const PROGRESS_INTERVAL_MS = 1000;
// detail-harvest polls search_positions until the (async) /search capture lands. Captures usually
// arrive in <1s; the ~6s ceiling only bounds the rare slow-WB / empty-result case.
const HARVEST_POLL_MS = 500;
const HARVEST_MAX_POLLS = 12;

let stopped = false;
let snapshot: SnapshotTs = '';
let detailK = DEFAULT_DETAIL_K; // top-N cards to /detail per query; (re)loaded from storage in runLoop
const counts: FactCounts = { ...EMPTY_COUNTS };
let progressDirty = false;
let harvested = new Set<number>();
// ranked nm_ids for this snapshot (built from decoded search_positions). competitor cards whose nm
// is NOT in here — and that carry a real query_id — are carousel/recommendation noise and are
// dropped before persist (see dropCascadeCards in offscreen/filter.ts).
let positionNm = new Set<number>();
// per-snapshot natural-key sets for fact-row dedup (see dedupeBySeen in db/write.ts)
let seen = freshSeen();

chrome.runtime.onMessage.addListener((msg: ToOffscreen | FromOffscreen | unknown) => {
  if (!msg || typeof msg !== 'object') return;
  const m = msg as ToOffscreen;
  switch (m.type) {
    case 'COLLECT_LOOP':
      stopped = false;
      snapshot = m.snapshotTs;
      detailK = m.detailK ?? DEFAULT_DETAIL_K; // arrives via message (SW read chrome.storage)
      console.log(`[Poncho] COLLECT_LOOP detailK=${detailK} targets=${m.targets.length}`);
      Object.assign(counts, EMPTY_COUNTS);
      harvested = new Set();
      positionNm = new Set();
      seen = freshSeen();
      startProgressTimer();
      runLoop(m.targets).catch((e) => console.error('[Poncho] collect loop', e));
      return;
    case 'MOCK_DECODE':
      // No-browser test hook: decode + persist synthetic intercepts (S3 DoD).
      stopped = false;
      snapshot = m.snapshotTs;
      Object.assign(counts, EMPTY_COUNTS);
      positionNm = new Set();
      seen = freshSeen();
      runMockDecode(m.intercepts).catch((e) => console.error('[Poncho] mock decode', e));
      return;
    case 'CAPTURE':
      void onCapture(m.item);
      return;
    case 'COLLECT_STOP':
      stopped = true;
      return;
  }
});

// ---------- mock decode (test hook) ----------
async function runMockDecode(intercepts: Intercept[]): Promise<void> {
  for (const it of intercepts) {
    if (stopped) break;
    await decodeAndPersist(it);
    sendProgress({ phase: `mock decode (${it.kind})` });
  }
  await sendProgress({ phase: 'mock done' });
  void chrome.runtime.sendMessage({ type: 'COLLECT_DONE' }).catch(() => {});
}

// ---------- live run loop ----------
async function runLoop(targets: Target[]): Promise<void> {
  // detailK is set at the COLLECT_LOOP handler from the message (the SW reads chrome.storage, which
  // is proven in the SW context — the offscreen no longer touches chrome.storage, only IndexedDB).
  try {
    for (const t of targets) {
      if (stopped) break;
      await runTarget(t);
    }
  } finally {
    // let the last target's in-flight intercepts land before the final progress
    await sleep(1500);
    stopProgressTimer();
    await flushProgress();
  }
  void chrome.runtime.sendMessage({ type: 'COLLECT_DONE' }).catch(() => {});
}

async function runTarget(t: Target): Promise<void> {
  console.log(`[Poncho] target ${t.kind} qid=${t.query_id ?? '∅'} ${t.url}`);
  sendProgress({ target: t, phase: 'navigate' });
  await sendToSw({ type: 'NAVIGATE', target: t });
  if (stopped) return;
  await sleep(1000 + rand(2000)); // 1-3s: page 1 loads, SPA fires the first search request

  // pages 2..MAX_PAGES come from scrolling (each scroll makes WB fetch /search?page=N)
  for (let p = 2; p <= MAX_PAGES; p++) {
    if (stopped) break;
    sendProgress({ target: t, phase: `scroll p${p}` });
    const res = (await sendToSw<{ ok?: boolean; grew?: boolean }>({ type: 'SCROLL' })) ?? {};
    if (res.grew === false) break; // page stopped growing → end of results
    await sleep(1000 + rand(2000));
  }

  // detail-harvest: open up to `detailK` competitor cards (configurable; <=0 = all) to capture
  // /detail (per-wh stock + promotions). Only search targets carry a real query_id with positions.
  if (t.kind === 'search' && t.query_id != null) {
    sendProgress({ target: t, phase: 'await positions' }); // pickHarvestNmids may wait for the /search capture
    const limit = detailK > 0 ? detailK : Infinity; // <=0 = no cap → detail every position
    const nmids = await pickHarvestNmids(t.query_id, limit);
    for (const nm of nmids) {
      if (stopped) break;
      sendProgress({ target: t, phase: `detail ${nm}` });
      await sendToSw({ type: 'NAVIGATE', target: { ...t, kind: 'card', query: '', url: detailUrl(nm) } });
      if (stopped) break;
      await sleep(1000 + rand(2000)); // let /detail + /list + card.json (wbbasket CDN content) fire
    }
  }
  sendProgress({ target: t, phase: 'done' });
}

/** Pick up to `limit` un-harvested nm_ids from this query's positions in the current snapshot.
 *  POLLS Dexie until the positions appear: the /search capture is persisted asynchronously
 *  (CAPTURE → decodeAndPersist), and with few results the scroll loop breaks early, so a one-shot
 *  read would race the capture and return [] → no /detail navigation. See pollUntilNonEmpty. */
async function pickHarvestNmids(queryId: number, limit: number): Promise<number[]> {
  const read = () => db.search_positions.where('[query_id+snapshot_ts]').equals([queryId, snapshot]).toArray();
  const rows = await pollUntilNonEmpty(read, () => stopped, sleep, HARVEST_POLL_MS, HARVEST_MAX_POLLS);
  return pickUnharvested(rows, harvested, limit);
}

// ---------- capture → decode → persist (the write path; SW-independent) ----------
async function onCapture(it: Intercept): Promise<void> {
  await decodeAndPersist(it);
  progressDirty = true;
}

async function decodeAndPersist(it: Intercept): Promise<void> {
  let decoded: Decoded;
  try {
    decoded = Decode(it, snapshot);
  } catch (e) {
    console.warn('[Poncho] decode failed (skipping capture):', (e as Error).message, it.url);
    return;
  }
  // Feed the ranked-nm set from search captures so card captures can be filtered against it. No DB
  // read needed — the positions we just decoded ARE the new ranked nm. For a resumed run (offscreen
  // reborn mid-session) where cards arrive before any search capture, hydrate once from Dexie.
  if (it.kind === 'search' || it.kind === 'brand') {
    for (const sp of decoded.search_positions) positionNm.add(sp.nm_id);
  } else if (positionNm.size === 0 && decoded.competitor_cards.length > 0) {
    await db.search_positions.where('snapshot_ts').equals(snapshot).each((r) => positionNm.add(r.nm_id));
  }
  // Drop carousel/recommendation cards (nm not ranked for this snapshot) BEFORE dedup, so carousel
  // nm never enters seen.cards. query_id=null (direct nmId/url targets) is always kept — its carousel
  // is legitimate competitor context for the target product.
  const filtered = dropCascadeCards(decoded, positionNm);
  // drop rows already persisted this snapshot (WB re-fires /list+/detail on scroll/nav); counting
  // the FRESH rows keeps the progress totals honest instead of inflated by duplicates.
  const fresh = dedupeBySeen(filtered, seen);
  counts.search_positions += fresh.search_positions.length;
  counts.vitrine_ads += fresh.vitrine_ads.length;
  counts.competitor_cards += fresh.competitor_cards.length;
  counts.competitor_card_prices += fresh.competitor_card_prices.length;
  counts.competitor_card_details += fresh.competitor_card_details.length;
  counts.competitor_card_stocks += fresh.competitor_card_stocks.length;
  counts.competitor_card_meta += fresh.competitor_card_meta.length;
  counts.competitor_card_options += fresh.competitor_card_options.length;
  counts.competitor_card_compositions += fresh.competitor_card_compositions.length;
  counts.competitor_card_sizes += fresh.competitor_card_sizes.length;
  counts.competitor_card_colors += fresh.competitor_card_colors.length;
  await persistDecoded(fresh);
}

// ---------- progress (throttled: a dirty flag + 1s timer; per-target phase fires immediately) ----------
function sendProgress(p: { target?: Target; phase: string }): void {
  void chrome.runtime.sendMessage({ type: 'PROGRESS', ...p, counts: { ...counts } }).catch(() => {});
}
async function flushProgress(): Promise<void> {
  sendProgress({ phase: stopped ? 'stopped' : 'idle' });
  progressDirty = false;
}
let progressTimer: ReturnType<typeof setInterval> | null = null;
function startProgressTimer(): void {
  stopProgressTimer();
  progressTimer = setInterval(() => {
    if (progressDirty) flushProgress();
  }, PROGRESS_INTERVAL_MS);
}
function stopProgressTimer(): void {
  if (progressTimer) {
    clearInterval(progressTimer);
    progressTimer = null;
  }
}

// ---------- helpers ----------
function sendToSw<T>(msg: FromOffscreen): Promise<T> {
  return chrome.runtime.sendMessage(msg) as Promise<T>;
}
function rand(ms: number): number {
  return Math.floor(Math.random() * ms);
}
function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

console.log('[Poncho] offscreen orchestrator ready');
