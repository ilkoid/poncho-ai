// background.js — MV3 service worker. Holds mode state, filters intercepts,
// buffers into IndexedDB (survives SW rebirth), exports via chrome.downloads, and
// orchestrates the Collect loop through an offscreen document (SW can't keep human-pace timers).

// ---------- runtime state ----------
let mode = 'recon';            // 'recon' | 'collect'
let collectTabId = null;       // dedicated background tab navigated per target
let collectRunning = false;
let offscreenReady = false;
// per-run set of nmIds already opened for detail harvest (avoids re-opening the same competitor
// card across overlapping search targets). Reset on each COLLECT_START; lost on SW rebirth, which
// only means "this run starts fresh" — fine for a single clean snapshot.
let harvestedNmids = new Set();

// SW can be killed & reborn mid-collect; recover persisted state on startup.
// Use local (always available via "storage" permission), not session — session can be
// unavailable in some builds and would crash the SW on load (top-level await-free call).
chrome.storage.local.get(['mode', 'collectTabId']).then((s) => {
  mode = s.mode || 'recon';
  collectTabId = s.collectTabId ?? null;
}).catch(() => {});

// ---------- Collect patterns (VERIFIED against wb-recon-*.json, 2026-06-28) ----------
// WB serves storefront APIs under www.wildberries.ru/__internal/... (same-origin proxy),
// NOT card.wb.ru / search.wb.ru. Versions are versioned (v4 cards, v18 search) and drift,
// so match /v\d+/. Re-verify after WB layout changes.
const COLLECT_PATTERNS = [
  { kind: 'card_detail', re: /\/__internal\/card\/cards\/v\d+\/detail\b/i },  // ONE card: characteristics / dimensions / per-wh qty (→ competitor_card_details)
  { kind: 'card_list',   re: /\/__internal\/card\/cards\/v\d+\/list\b/i },    // batch hydration: same shape as search (no characteristics)
  { kind: 'search', re: /\/__internal\/search\/exactmatch\/[^/]+\/common\/v\d+\/search\b/i }, // positions (served as text/plain!)
  { kind: 'ad',     re: /(\/__internal\/banners\/shelfs\/search|banners-website\.wildberries\.ru)/i }, // ads: cpm / erid / products
  { kind: 'brand',  re: /\/__internal\/catalog\/brands\/v\d+\/(catalog|filters)\b/i },   // brand page catalog + facets
  // promo NOT yet mapped: only /webapi/spa/promotions/metatags seen — needs dedicated Recon on a promo page.
];

function classify(url) {
  for (const p of COLLECT_PATTERNS) if (p.re.test(url)) return p.kind;
  return null;
}

// ---------- persistent intercept buffers via IndexedDB ----------
// IndexedDB (not chrome.storage, not in-memory): survives SW death/rebirth AND browser
// restart, and has effectively no quota — fits WB's multi-MB dumps. chrome.storage capped
// at ~5MB ("Resource::kQuotaBytes quota exceeded"); in-memory vanished on SW rebirth —
// which was exactly why Recon data disappeared when the user navigated between pages.
const DB_NAME = 'wb-scraper';
const DB_VERSION = 1;
let dbPromise = null;

function db() {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const idb = req.result;
      if (!idb.objectStoreNames.contains('recon')) idb.createObjectStore('recon', { autoIncrement: true });
      if (!idb.objectStoreNames.contains('collect')) idb.createObjectStore('collect', { autoIncrement: true });
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

function idbRun(store, txnMode, fn) {
  return db().then((conn) => new Promise((resolve, reject) => {
    const tx = conn.transaction(store, txnMode);
    let result;
    const req = fn(tx.objectStore(store));
    if (req) req.onsuccess = () => { result = req.result; };
    tx.oncomplete = () => resolve(result);
    tx.onerror = () => reject(tx.error);
  }));
}

const idbAdd = (store, item) => idbRun(store, 'readwrite', (s) => s.add(item));
const idbGetAll = (store) => idbRun(store, 'readonly', (s) => s.getAll());
const idbCount = (store) => idbRun(store, 'readonly', (s) => s.count());
const idbClear = (store) => idbRun(store, 'readwrite', (s) => s.clear());

async function onIntercept(payload) {
  const url = (payload && payload.url) || '';
  try {
    if (mode === 'recon') {
      await idbAdd('recon', payload);
      console.log('[WB Scraper] recon+', url.slice(0, 80));
    } else {
      const kind = classify(url);
      if (kind) {
        await idbAdd('collect', { ...payload, kind });
        console.log('[WB Scraper] collect ' + kind, url.slice(0, 80));
      } else {
        console.log('[WB Scraper] collect (skip)', url.slice(0, 80));
      }
    }
  } catch (e) {
    console.error('[WB Scraper] idb intercept', e);
  }
}

// ---------- export ----------
function stamp() {
  const d = new Date();
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`;
}

async function exportBuffer(kind, prefix) {
  const data = await idbGetAll(kind).catch(() => []);
  // Self-describe the snapshot so a dump is identifiable without opening it: the search queries
  // it covers (from captured metadata.name) + distinct nmIds captured via /detail. For nmId-only
  // runs `queries` is empty (then `detailNmIds` carries the identity).
  const queries = [...new Set(
    data.filter((it) => it.kind === 'search' && it.body && it.body.metadata && it.body.metadata.name)
        .map((it) => it.body.metadata.name)
  )];
  const detailNmIds = [...new Set(
    data.filter((it) => it.kind === 'card_detail' && Array.isArray(it.body && it.body.products))
        .flatMap((it) => it.body.products.map((p) => p && p.id))
        .filter((id) => id != null)
  )];
  // card→query→position binding: flatten search captures into explicit rows so the binding is
  // visible without jq and is a ready fixture for the collector's search_positions table.
  // Global position = (page-1)*PAGE_SIZE + index + 1 (WB returns 100/page; verified on dumps).
  const PAGE_SIZE = 100;
  const positions = [];
  for (const it of data) {
    if (it.kind !== 'search') continue;
    const prods = it.body && it.body.products;
    if (!Array.isArray(prods)) continue;          // skip resultset=filters (no products)
    const query = it.body.metadata && it.body.metadata.name;
    const url = (it.payload && it.payload.url) || it.url || '';
    const pageM = url.match(/[?&]page=(\d+)/);
    const destM = url.match(/[?&]dest=(\d+)/);
    const page = pageM ? +pageM[1] : 1;
    const dest = destM ? +destM[1] : null;
    prods.forEach((p, idx) => {
      if (!p) return;
      const price = p.sizes && p.sizes[0] && p.sizes[0].price;
      positions.push({
        query, region_dest: dest, page,
        position: (page - 1) * PAGE_SIZE + idx + 1,
        nm_id: p.id, brand: p.brand, supplier_id: p.supplierId,
        panel_promo_id: p.panelPromoId != null ? p.panelPromoId : null,
        price_basic: price && price.basic,
        price_product: price && price.product,
        rating: p.rating, feedbacks: p.feedbacks,
      });
    });
  }
  const json = JSON.stringify(
    { generatedAt: new Date().toISOString(), mode: kind, count: data.length,
      queries, detailNmIds: detailNmIds.length, positions, items: data },
    null, 2
  );
  // UTF-8 safe base64 (cyrillic product names survive); data: URL works reliably in SW
  const b64 = btoa(unescape(encodeURIComponent(json)));
  const dataUrl = `data:application/json;base64,${b64}`;
  chrome.downloads.download({ url: dataUrl, filename: `${prefix}-${stamp()}.json`, saveAs: false })
    .catch((e) => console.error('[WB Scraper] export failed', e));
}

// ---------- offscreen lifecycle (needed only for Collect) ----------
async function ensureOffscreen() {
  if (offscreenReady) return;
  try {
    if (chrome.offscreen.hasDocument && (await chrome.offscreen.hasDocument())) {
      offscreenReady = true;
      console.log('[WB Scraper] offscreen already exists');
      return;
    }
  } catch { /* older Chrome: no hasDocument */ }
  try {
    await chrome.offscreen.createDocument({
      url: 'src/offscreen.html',
      reasons: ['WORKERS'],
      justification: 'Background loop with setTimeout to pace WB tab navigations at human speed (2-7s pauses); chrome.alarms minimum is 30s, too coarse.',
    });
    offscreenReady = true;
    console.log('[WB Scraper] offscreen created');
  } catch (e) {
    console.error('[WB Scraper] offscreen create FAILED — Collect will not run:', e);
  }
}

// ---------- collect tab navigation (rebirth-safe) ----------
// SW state is volatile: if it dies mid-collect, `collectTabId` resets to null.
// Recover from session storage, and recreate the tab if the user closed it.
async function navigateCollectTab(url) {
  console.log('[WB Scraper] NAVIGATE ->', url.slice(0, 70));
  if (collectTabId == null) {
    const s = await chrome.storage.local.get('collectTabId').catch(() => ({}));
    collectTabId = s.collectTabId ?? null;
  }
  if (collectTabId != null) {
    try {
      await chrome.tabs.update(collectTabId, { url });
      return;
    } catch { /* tab gone — recreate below */ }
  }
  const tab = await chrome.tabs.create({ url, active: false });
  collectTabId = tab.id;
  await chrome.storage.local.set({ collectTabId: tab.id }).catch(() => {});
}

// ---------- message router ----------
chrome.runtime.onMessage.addListener((msg, sender, reply) => {
  if (!msg || !msg.type) return;

  switch (msg.type) {
    case 'INTERCEPT':
      onIntercept(msg.payload);
      return;

    case 'RECON_OPEN':
      mode = 'recon';
      chrome.storage.local.set({ mode }).then(() => chrome.tabs.create({ url: msg.url }));
      return;

    case 'COLLECT_START':
      (async () => {
        console.log('[WB Scraper] COLLECT_START', (msg.targets || []).length, 'targets');
        mode = 'collect';
        collectRunning = true;
        await idbClear('collect').catch(() => {}); // fresh snapshot per run — "one run = one clean file"
        harvestedNmids = new Set(); // also reset per-run detail-harvest dedup
        await chrome.storage.local.set({ mode });
        const tab = await chrome.tabs.create({ url: 'https://www.wildberries.ru', active: true });
        console.log('[WB Scraper] collect tab', tab.id, 'opened (active — avoids background-tab throttling)');
        collectTabId = tab.id;
        await chrome.storage.local.set({ collectTabId: tab.id }).catch(() => {});
        await ensureOffscreen();
        // offscreen script may not have attached its listener yet — retry until it answers
        for (let attempt = 0; attempt < 10; attempt++) {
          try {
            await chrome.runtime.sendMessage({ type: 'COLLECT_LOOP', targets: msg.targets });
            return;
          } catch {
            await new Promise((r) => setTimeout(r, 200));
          }
        }
        console.error('[WB Scraper] offscreen never answered COLLECT_LOOP');
      })();
      return;

    case 'COLLECT_STOP':
      collectRunning = false;
      chrome.runtime.sendMessage({ type: 'COLLECT_STOP' }).catch(() => {});
      return;

    case 'NAVIGATE': // from offscreen
      navigateCollectTab(msg.url).catch((e) => console.warn('[WB Scraper] navigate', e));
      return;

    case 'SCROLL': // from offscreen → forward to the content script → relay its {grew} reply back
      if (collectTabId != null) {
        chrome.tabs.sendMessage(collectTabId, { type: 'SCROLL' }, (contentReply) => {
          reply(contentReply || { ok: false, grew: false });
        });
      } else {
        reply({ ok: false, grew: false });
      }
      return true; // async — wait for the content script's reply

    case 'GET_NMIDS': // from offscreen — detail-harvest wants K un-harvested competitor nmIds
      (async () => {
        const limit = msg.limit || 10;
        const items = await idbGetAll('collect').catch(() => []);
        const picked = [];
        const seen = new Set();
        for (const it of items) {
          if (it.kind !== 'search') continue;            // only competitor nmIds from search results
          const prods = it.body && it.body.products;
          if (!Array.isArray(prods)) continue;            // skip resultset=filters (no products)
          for (const p of prods) {
            const id = p && p.id;
            if (id == null || harvestedNmids.has(id) || seen.has(id)) continue;
            seen.add(id);
            picked.push(id);
            if (picked.length >= limit) break;
          }
          if (picked.length >= limit) break;
        }
        picked.forEach((id) => harvestedNmids.add(id));  // mark so later targets skip them
        console.log(`[WB Scraper] GET_NMIDS → ${picked.length} nmId(s) for detail harvest`);
        reply({ nmids: picked });
      })();
      return true; // async reply

    case 'COLLECT_DONE': // from offscreen
      collectRunning = false;
      console.log('[WB Scraper] collect loop finished');
      return;

    case 'EXPORT': {
      const kind = mode === 'collect' ? 'collect' : 'recon';
      const prefix = mode === 'collect' ? 'wb-scrape' : 'wb-recon';
      exportBuffer(kind, prefix).catch((e) => console.error('[WB Scraper] export', e));
      return;
    }

    case 'CLEAR':
      Promise.all([idbClear('recon'), idbClear('collect')]).catch(() => {});
      chrome.storage.local.remove(['recon_buffer', 'collect_buffer']).catch(() => {}); // legacy storage
      console.log('[WB Scraper] buffers cleared');
      return;

    case 'SET_MODE':
      mode = msg.mode;
      chrome.storage.local.set({ mode });
      return;

    case 'GET_STATE':
      Promise.all([idbCount('recon'), idbCount('collect')])
        .then(([r, c]) => reply({ mode, reconCount: r || 0, collectCount: c || 0, collectRunning }))
        .catch(() => reply({ mode, reconCount: 0, collectCount: 0, collectRunning }));
      return true; // async reply
  }
});

console.log('[WB Scraper] background service worker started');
