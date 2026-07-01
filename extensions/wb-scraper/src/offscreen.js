// offscreen.js — lives in a hidden document so setTimeout is reliable (SW dies after ~30s idle,
// chrome.alarms has a 30s minimum, but we need human-pace 2-7s pauses between navigations).
//
// Simple version: for each target, tell the SW to navigate, then sleep a random 2-7s, then next.
// We do NOT wait for a confirmation that the card endpoint was intercepted — the SW stores every
// matched response into collect_buffer in parallel. Good enough for a prototype; precise
// target->response coupling (NAVIGATE then await kind=card DONE) is a later refinement.

let stopped = false;

// Pages captured per target: page 1 comes from NAVIGATE; pages 2..MAX_PAGES come from scrolling
// (each scroll makes WB's SPA fetch the same /search? endpoint with page=N — inject catches it).
// Loop also stops early when the page stops growing (small queries exhaust before MAX_PAGES).
const MAX_PAGES = 3;

// After scrolling a search/brand target, open this many competitor cards to capture /detail
// (per-warehouse stock + promotions — exclusive to /detail, absent in /list). Each opened card page
// also fires /detail for its model group + similar products (~8-16 nmIds), so DETAIL_K=8 effectively
// covers ~60-130 cards per target. Card/url targets skip this (user chose those cards explicitly).
const DETAIL_K = 8;

chrome.runtime.onMessage.addListener((msg, sender, reply) => {
  if (msg && msg.type === 'COLLECT_LOOP') {
    stopped = false;
    runLoop(msg.targets || []).catch((e) => console.error('[WB Scraper] collect loop', e));
    return; // fire and forget
  }
  if (msg && msg.type === 'COLLECT_STOP') {
    stopped = true;
  }
});

async function runLoop(targets) {
  for (let i = 0; i < targets.length; i++) {
    if (stopped) break;
    const t = targets[i];
    console.log(`[WB Scraper] navigate ${i + 1}/${targets.length}: ${t.kind || ''} ${t.url}`);
    await chrome.runtime.sendMessage({ type: 'NAVIGATE', url: t.url }).catch(() => {});
    if (stopped) break;
    // wait for page 1 to load (SPA fires the first search request)
    await sleep(1000 + Math.floor(Math.random() * 2000)); // 1-3s
    // scroll for pages 2..MAX_PAGES — triggers WB's lazy loader; inject intercepts each page.
    // Stop early if the page stopped growing (end of results — e.g. small queries with <100 items).
    for (let p = 2; p <= MAX_PAGES; p++) {
      if (stopped) break;
      console.log(`[WB Scraper] scroll page ${p}/${MAX_PAGES} for target ${i + 1}/${targets.length}`);
      const res = await chrome.runtime.sendMessage({ type: 'SCROLL' }).catch(() => ({}));
      if (!res || res.grew === false) {
        console.log(`[WB Scraper] end of results for target ${i + 1}/${targets.length} — stop scrolling`);
        break;
      }
      await sleep(1000 + Math.floor(Math.random() * 2000)); // 1-3s human pace + wait for response
    }
    // detail harvest: open top-K competitor cards captured in this target's search results.
    // Each card page fires /detail (stock + promo) + /list (similar) — both intercepted.
    if (t.kind === 'search' || t.kind === 'brand') {
      const got = await chrome.runtime.sendMessage({ type: 'GET_NMIDS', limit: DETAIL_K }).catch(() => ({}));
      const nmids = (got && got.nmids) || [];
      for (let d = 0; d < nmids.length; d++) {
        if (stopped) break;
        console.log(`[WB Scraper] detail ${d + 1}/${nmids.length} (nmId ${nmids[d]}) for target ${i + 1}/${targets.length}`);
        await chrome.runtime.sendMessage({
          type: 'NAVIGATE',
          url: `https://www.wildberries.ru/catalog/${nmids[d]}/detail.aspx`,
        }).catch(() => {});
        if (stopped) break;
        await sleep(1000 + Math.floor(Math.random() * 2000)); // 1-3s human pace; let /detail + /list fire
      }
    }
  }
  await chrome.runtime.sendMessage({ type: 'COLLECT_DONE' }).catch(() => {});
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}
