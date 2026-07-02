// offscreen.js — lives in a hidden document so setTimeout is reliable (the MV3 service worker
// dies after ~30s idle, and chrome.alarms has a 30s floor, but we need human-pace 1-3s pauses).
//
// Stage 6: the loop is now PULL-driven. It fetches target batches from the Go collector
// (GET /targets) until the collector signals done, processes each target with the existing
// capture sequence (navigate → scroll → detail-harvest), and pushes intercepted WB responses
// back to the collector in batches (POST /capture). The popup may still hand a fixed target
// list as a manual/debug override; when it does, the loop iterates that list with no HTTP.
//
// Why the offscreen owns the HTTP (not the SW): it is the stable context — it survives for the
// whole collect run, so the batch flush timer is reliable. The SW, by contrast, can be killed
// mid-second. Captures arrive here from the SW as CAPTURE messages (one per intercepted WB
// response, already stamped with query_id by the SW); this doc batches them so /capture stays
// low-volume.

let stopped = false;
let endpoint = ''; // collector base URL, e.g. http://127.0.0.1:7780 (set per COLLECT_LOOP)

// Pages captured per target: page 1 comes from NAVIGATE; pages 2..MAX_PAGES come from scrolling
// (each scroll makes WB's SPA fetch the same /search? endpoint with page=N — inject catches it).
// Loop also stops early when the page stops growing (small queries exhaust before MAX_PAGES).
const MAX_PAGES = 3;

// After scrolling a search/brand target, open this many competitor cards to capture /detail
// (per-warehouse stock + promotions — exclusive to /detail, absent in /list). Each opened card page
// also fires /detail for its model group + similar products (~8-16 nmIds), so DETAIL_K=8 effectively
// covers ~60-130 cards per target. Card/url targets skip this (user chose those cards explicitly).
const DETAIL_K = 8;

// ---- collector HTTP knobs ----
const TARGETS_BATCH = 50; // items requested per GET /targets pull (server may cap lower)
const FLUSH_COUNT = 20; // flush the capture buffer after this many buffered responses
const FLUSH_INTERVAL_MS = 3000; // …or after this long, whichever comes first
const DRAIN_SETTLE_MS = 2500; // pause before the final flush so in-flight intercepts land

// ---- capture buffer (drained to POST /capture) ----
let captureBuf = [];
let flushTimer = null;
let flushing = false;

chrome.runtime.onMessage.addListener((msg) => {
  if (!msg) return;
  if (msg.type === 'COLLECT_LOOP') {
    stopped = false;
    endpoint = msg.endpoint || '';
    captureBuf = [];
    runLoop({ targets: msg.targets || [] }).catch((e) => console.error('[WB Scraper] collect loop', e));
    return; // fire and forget
  }
  if (msg.type === 'CAPTURE') {
    // SW-forwarded intercept (one WB response). Buffer it; maybe flush on the count threshold.
    if (msg.item) {
      captureBuf.push(msg.item);
      maybeFlush();
    }
    return;
  }
  if (msg.type === 'COLLECT_STOP') {
    stopped = true;
  }
});

// runLoop drives one collect session. With a popup-provided target list it runs manual (no HTTP);
// otherwise it pulls from the collector until done. The finally block always drains the buffer and
// — in pull mode — POSTs /done, then tells the SW the loop finished.
async function runLoop({ targets }) {
  startFlushTimer();
  try {
    if (targets.length) {
      for (const t of targets) {
        if (stopped) break;
        await runTarget(normalizeTarget(t));
      }
    } else if (endpoint) {
      await runPull();
    } else {
      console.warn('[WB Scraper] COLLECT_LOOP with no targets and no endpoint — nothing to do');
    }
  } finally {
    // Let the very last target's in-flight intercepts reach the SW and get forwarded here, then
    // drain everything. Manual mode (no endpoint) skips the wait — there is no server to push to.
    if (endpoint && !stopped) await sleep(DRAIN_SETTLE_MS);
    await flushCaptures();
    if (endpoint && !stopped) await postDone();
    stopFlushTimer();
  }
  await chrome.runtime.sendMessage({ type: 'COLLECT_DONE' }).catch(() => {});
}

// runPull drains the collector's target queue in batches. Each batch's items are processed with the
// same per-target steps as manual mode. A transient /targets failure backs off and retries rather
// than aborting the session (the collector may be restarting).
async function runPull() {
  while (!stopped) {
    let batch;
    try {
      batch = await fetchTargets();
    } catch (e) {
      console.warn('[WB Scraper] GET /targets failed, retrying in 3s:', e.message);
      await sleep(3000);
      continue;
    }
    const items = batch.items || [];
    if (!items.length) break; // queue exhausted
    console.log(`[WB Scraper] /targets batch ${items.length}, served ${batch.served}/${batch.total}`);
    for (const t of items) {
      if (stopped) break;
      await runTarget(normalizeTarget(t));
    }
    if (batch.done) break;
  }
}

// runTarget performs the per-target capture sequence: navigate (page 1), scroll for pages 2..MAX_PAGES,
// then detail-harvest K competitor cards. Detail navigates INHERIT the parent target's query_id, so
// /detail captures bind back to the originating search query (the competitor's rank context).
async function runTarget(t) {
  console.log(`[WB Scraper] target ${t.kind} qid=${t.query_id ?? 0} ${t.url}`);
  await chrome.runtime.sendMessage({ type: 'NAVIGATE', target: t }).catch(() => {});
  if (stopped) return;
  await sleep(1000 + rand(2000)); // 1-3s: page 1 loads, SPA fires the first search request
  for (let p = 2; p <= MAX_PAGES; p++) {
    if (stopped) break;
    const res = await chrome.runtime.sendMessage({ type: 'SCROLL' }).catch(() => ({}));
    if (!res || res.grew === false) {
      // page stopped growing → end of results for this target
      break;
    }
    await sleep(1000 + rand(2000)); // 1-3s human pace + wait for the next page response
  }
  if (t.kind === 'search' || t.kind === 'brand') {
    const got = await chrome.runtime.sendMessage({ type: 'GET_NMIDS', limit: DETAIL_K }).catch(() => ({}));
    const nmids = (got && got.nmids) || [];
    for (const nmid of nmids) {
      if (stopped) break;
      await chrome.runtime.sendMessage({
        type: 'NAVIGATE',
        target: { kind: 'card', query_id: t.query_id || 0, url: `https://www.wildberries.ru/catalog/${nmid}/detail.aspx` },
      }).catch(() => {});
      if (stopped) break;
      await sleep(1000 + rand(2000)); // 1-3s: let /detail + /list fire
    }
  }
}

// normalizeTarget coerces a target from either source into the shape runTarget expects. Popup
// manual lines arrive as {kind,url} (no query_id → NoQuery); /targets items arrive fully formed.
function normalizeTarget(t) {
  if (!t) return { kind: 'search', query_id: 0, query: '', url: '' };
  return {
    kind: t.kind || 'search',
    query_id: t.query_id || 0,
    query: t.query || '',
    url: t.url || '',
  };
}

// ---------- collector HTTP ----------

async function fetchTargets() {
  const r = await fetch(`${endpoint}/targets?n=${TARGETS_BATCH}`);
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

// flushCaptures posts the buffered captures and clears them on success. The `flushing` guard
// prevents overlapping flushes (count-threshold + timer could fire together). On a transport
// failure (collector down) the batch is re-queued at the front so nothing is lost while the
// collector restarts; the next tick retries.
async function flushCaptures() {
  if (!endpoint || !captureBuf.length || flushing) return;
  flushing = true;
  const batch = captureBuf;
  captureBuf = [];
  try {
    const r = await fetch(`${endpoint}/capture`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(batch),
    });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const j = await r.json().catch(() => ({}));
    const d = j.decoded || {};
    console.log(
      `[WB Scraper] POST /capture ${batch.length} → accepted ${j.accepted}` +
        ` (pos ${d.positions || 0} ads ${d.ads || 0} cards ${d.cards || 0} prices ${d.prices || 0})`
    );
  } catch (e) {
    console.warn(`[WB Scraper] /capture failed, re-queuing ${batch.length}:`, e.message);
    captureBuf.unshift(...batch);
  } finally {
    flushing = false;
  }
}

function maybeFlush() {
  if (captureBuf.length >= FLUSH_COUNT) flushCaptures();
}

function startFlushTimer() {
  stopFlushTimer();
  flushTimer = setInterval(() => {
    if (captureBuf.length) flushCaptures();
  }, FLUSH_INTERVAL_MS);
}

function stopFlushTimer() {
  if (flushTimer) {
    clearInterval(flushTimer);
    flushTimer = null;
  }
}

// postDone signals the collector that the session is complete; it triggers the server's final
// flush so the operator sees terminal counts before tearing down.
async function postDone() {
  try {
    const r = await fetch(`${endpoint}/done`, { method: 'POST' });
    const j = await r.json().catch(() => ({}));
    console.log('[WB Scraper] POST /done → flushed', JSON.stringify(j.flushed || {}));
  } catch (e) {
    console.warn('[WB Scraper] /done failed:', e.message);
  }
}

function rand(ms) {
  return Math.floor(Math.random() * ms);
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}
