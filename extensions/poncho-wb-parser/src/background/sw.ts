// src/background/sw.ts — MV3 service worker (thin router + offscreen lifecycle).
//
// The SW is volatile (Chrome kills it ~30s idle), so it NEVER owns the run-loop or Dexie writes —
// those live in the offscreen document (src/offscreen/orchestrator.ts). The SW only:
//   - builds targets (resolving query_ids) and hands COLLECT_LOOP / MOCK_DECODE to the offscreen,
//   - stamps the active target's query_id onto MAIN-world intercepts and forwards CAPTURE to offscreen,
//   - relays offscreen NAVIGATE/SCROLL to the collect tab,
//   - answers GET_STATE (Dexie reads are stateless → rebirth-safe).
// Because writes are in the offscreen, killing the SW mid-session cannot lose data: the offscreen
// keeps pacing + persisting, and a new SW auto-restarts on the next chrome.runtime message.

import { db } from '../db/dexie';
import { classify } from '../decode/kind';
import { mockIntercepts } from '../storage/mock';
import { buildTargets } from '../querygen/targets';
import { loadDetailK } from '../storage/config';
import type { Intercept, SnapshotTs } from '../db/types';
import type { CollectSource, FromOffscreen, ToSW } from '../messages';

// ---------- rebirth-recoverable runtime state ----------
let mode: 'collect' | 'idle' = 'idle';
let collectTabId: number | null = null;
let running = false;
let snapshotTs: SnapshotTs | null = null;
/** Active target's query_id, used to stamp intercepts. Recovered from storage on SW rebirth. */
let currentQueryId: number | null = null;

chrome.storage.local
  .get(['mode', 'collectTabId', 'currentQueryId'])
  .then((s) => {
    mode = (s.mode as 'collect' | 'idle') || 'idle';
    collectTabId = (s.collectTabId as number | null) ?? null;
    currentQueryId = (s.currentQueryId as number | null) ?? null;
  })
  .catch(() => {});

// ---------- dashboard ----------
async function openDashboard(): Promise<void> {
  const url = chrome.runtime.getURL('src/dashboard/dashboard.html');
  const existing = await chrome.tabs.query({ url });
  if (existing.length > 0 && existing[0]?.id != null) {
    await chrome.tabs.update(existing[0].id, { active: true });
    await chrome.windows.update(existing[0].windowId, { focused: true });
    return;
  }
  await chrome.tabs.create({ url });
}

// ---------- offscreen lifecycle ----------
let offscreenReady = false;
async function ensureOffscreen(): Promise<void> {
  if (offscreenReady) return;
  try {
    if (chrome.offscreen.hasDocument && (await chrome.offscreen.hasDocument())) {
      offscreenReady = true;
      return;
    }
  } catch {
    /* older Chrome: no hasDocument */
  }
  try {
    await chrome.offscreen.createDocument({
      url: 'src/offscreen/offscreen.html',
      reasons: ['WORKERS'],
      justification: 'Background loop with setTimeout to pace WB tab navigations at human speed (1-3s pauses); chrome.alarms minimum is 30s, too coarse.',
    });
    offscreenReady = true;
  } catch (e) {
    console.error('[Poncho] offscreen create FAILED — collect will not run:', e);
  }
}

async function sendToOffscreen(msg: unknown, retries = 10): Promise<boolean> {
  for (let attempt = 0; attempt < retries; attempt++) {
    try {
      await chrome.runtime.sendMessage(msg);
      return true;
    } catch {
      await new Promise((r) => setTimeout(r, 200)); // offscreen listener may not be attached yet
    }
  }
  console.error('[Poncho] offscreen never answered', (msg as { type?: string }).type);
  return false;
}

// ---------- collect tab navigation (rebirth-safe) ----------
async function navigateCollectTab(url: string): Promise<void> {
  if (collectTabId == null) {
    const s = await chrome.storage.local.get('collectTabId').catch(() => ({}) as Record<string, unknown>);
    collectTabId = (s.collectTabId as number | null) ?? null;
  }
  if (collectTabId != null) {
    try {
      await chrome.tabs.update(collectTabId, { url });
      return;
    } catch {
      /* tab gone — recreate below */
    }
  }
  const tab = await chrome.tabs.create({ url, active: true }); // active: avoid background-tab throttling
  collectTabId = tab.id ?? null;
  await chrome.storage.local.set({ collectTabId }).catch(() => {});
}

// ---------- intercept → capture (stamp query_id, forward to offscreen) ----------
async function onIntercept(payload: { url: string; status: number; body: unknown }): Promise<void> {
  const kind = classify(payload.url);
  if (!kind) return; // not a storefront endpoint we collect — skip silently
  const item: Intercept = {
    kind,
    url: payload.url,
    query_id: currentQueryId, // null = direct nmId/url target (NoQuery sentinel)
    status: payload.status,
    body: payload.body,
  };
  void chrome.runtime.sendMessage({ type: 'CAPTURE', item }).catch(() => {});
}

// ---------- message router ----------
chrome.runtime.onMessage.addListener((msg: ToSW | FromOffscreen | unknown, _sender, reply: (r: unknown) => void) => {
  if (!msg || typeof msg !== 'object') return;
  const m = msg as ToSW | FromOffscreen;
  switch (m.type) {
    case 'OPEN_DASHBOARD':
      void openDashboard().catch((e) => console.error('[Poncho] openDashboard', e));
      return;

    case 'COLLECT_START':
      void startCollect(m.collect, m.snapshotTs);
      return;

    case 'COLLECT_STOP':
      running = false;
      mode = 'idle';
      void chrome.storage.local.set({ mode }).catch(() => {});
      void chrome.runtime.sendMessage({ type: 'COLLECT_STOP' }).catch(() => {});
      return;

    case 'RUN_MOCK_SESSION':
      void runMock(m.snapshotTs);
      return;

    case 'INTERCEPT':
      void onIntercept(m.payload);
      return;

    case 'NAVIGATE': // from offscreen — sets the active target's query_id, then navigates the tab
      currentQueryId = m.target.query_id;
      void chrome.storage.local.set({ currentQueryId }).catch(() => {});
      void navigateCollectTab(m.target.url).catch((e) => console.warn('[Poncho] navigate', e));
      return;

    case 'SCROLL': // from offscreen → forward to the content script, relay its {grew} reply
      if (collectTabId != null) {
        chrome.tabs.sendMessage(collectTabId, { type: 'SCROLL' }, (contentReply) => {
          reply(contentReply ?? { ok: false, grew: false });
        });
      } else {
        reply({ ok: false, grew: false });
      }
      return true; // async reply

    case 'COLLECT_DONE': // from offscreen
      running = false;
      mode = 'idle';
      void chrome.storage.local.set({ mode }).catch(() => {});
      console.log('[Poncho] collect loop finished');
      return;

    case 'CLEAR_ALL':
      void db.transaction('rw', db.tables, async () => {
        await Promise.all(db.tables.map((t) => t.clear()));
      }).catch(() => {});
      return;
  }
});

async function startCollect(collect: CollectSource, ts: string): Promise<void> {
  const targets = await buildTargets(collect);
  if (targets.length === 0) {
    console.warn('[Poncho] COLLECT_START with no targets — nothing to do (constructor source lands in S4)');
    return;
  }
  running = true;
  mode = 'collect';
  snapshotTs = ts;
  currentQueryId = null;
  await chrome.storage.local.set({ mode, currentQueryId }).catch(() => {});
  const detailK = await loadDetailK(); // SW context has proven chrome.storage access (unlike offscreen)
  await ensureOffscreen();
  await sendToOffscreen({ type: 'COLLECT_LOOP', targets, snapshotTs: ts, detailK });
}

async function runMock(ts: string): Promise<void> {
  running = true;
  mode = 'collect';
  snapshotTs = ts;
  await chrome.storage.local.set({ mode }).catch(() => {});
  await ensureOffscreen();
  await sendToOffscreen({ type: 'MOCK_DECODE', intercepts: mockIntercepts(), snapshotTs: ts });
}

chrome.runtime.onInstalled.addListener(() => {
  console.log('[Poncho] WB Parser installed (v0.1.0)');
});

console.log('[Poncho] service worker started');
