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
import { KEY_SCHEDULE_TIMES, loadDetailK } from '../storage/config';
import { enqueueShipment, shipPending, type PushResult } from '../export/push';
import { isScheduledAlarm, rebuildSchedule } from './schedule';
import type { Intercept, SnapshotTs } from '../db/types';
import type { CollectSource, FromOffscreen, ShipmentMsg, ToSW } from '../messages';

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

    case 'COLLECT_DONE': // from offscreen — ship the finished snapshot to the Go collector
      running = false;
      mode = 'idle';
      void chrome.storage.local.set({ mode }).catch(() => {});
      console.log('[Poncho] collect loop finished');
      void finishCollect().catch((e) => console.warn('[Poncho] shipment', e));
      return;

    case 'CLEAR_ALL':
      void db.transaction('rw', db.tables, async () => {
        await Promise.all(db.tables.map((t) => t.clear()));
      }).catch(() => {});
      return;
  }
});

async function startCollect(collect: CollectSource, ts: string): Promise<void> {
  // Guard against overlapping runs: a scheduled alarm firing during a manual collect (or a
  // double-click) must NOT clobber the in-flight session — skip silently instead.
  if (running || mode === 'collect') {
    console.warn('[Poncho] collect already running — skipping this start');
    return;
  }
  const targets = await buildTargets(collect);
  if (targets.length === 0) {
    console.warn('[Poncho] COLLECT_START with no targets — nothing to do (constructor source lands in S4)');
    return;
  }
  running = true;
  mode = 'collect';
  snapshotTs = ts;
  currentQueryId = null;
  await chrome.storage.local.set({ mode, currentQueryId, activeSnapshotTs: ts }).catch(() => {});
  const detailK = await loadDetailK(); // SW context has proven chrome.storage access (unlike offscreen)
  await ensureOffscreen();
  await sendToOffscreen({ type: 'COLLECT_LOOP', targets, snapshotTs: ts, detailK });
}

async function runMock(ts: string): Promise<void> {
  running = true;
  mode = 'collect';
  snapshotTs = ts;
  await chrome.storage.local.set({ mode, activeSnapshotTs: ts }).catch(() => {});
  await ensureOffscreen();
  await sendToOffscreen({ type: 'MOCK_DECODE', intercepts: mockIntercepts(), snapshotTs: ts });
}

chrome.runtime.onInstalled.addListener(() => {
  console.log('[Poncho] WB Parser installed (v0.1.0)');
  // Build the daily alarms from the saved schedule (covers both fresh install and version update).
  // Alarms persist across SW death afterwards; storage.onChanged rebuilds them on any schedule edit.
  void rebuildSchedule().catch((e) => console.warn('[Poncho] schedule rebuild', e));
});

// ---------- scheduled collects (chrome.alarms → startCollect) ----------
// A scheduled alarm fires daily; onAlarm is a wakeable event that restarts the SW to run the same
// collect chain as the dashboard button. startCollect's guard skips if a session is already running.
chrome.alarms.onAlarm.addListener((a) => {
  if (!isScheduledAlarm(a.name)) return;
  const ts = new Date().toISOString();
  console.log(`[Poncho] scheduled collect fired (${a.name}) → snapshot ${ts}`);
  void startCollect({ source: 'constructor' }, ts).catch((e) => console.warn('[Poncho] scheduled collect', e));
});

// Rebuild the alarms when the user edits the schedule in the dashboard. storage.onChanged is a
// wakeable event, so this rebuilds even if the SW was idle when the save happened.
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === 'local' && changes[KEY_SCHEDULE_TIMES]) {
    void rebuildSchedule().catch((e) => console.warn('[Poncho] schedule rebuild', e));
  }
});

// ---------- snapshot shipment (→ Go collector POST /snapshot) ----------
// On COLLECT_DONE the just-finished snapshot is enqueued for shipment then pushed; failures stay
// queued and are retried on the next COLLECT_DONE and on SW start (sweep). Browser-only when no
// server URL is configured — pushSnapshot no-ops then, and the snapshot leaves the queue at once.

/** Enqueue the finished session's snapshot, clear the active marker, then push the whole queue. */
async function finishCollect(): Promise<void> {
  const s = await chrome.storage.local.get('activeSnapshotTs').catch(() => ({}) as Record<string, unknown>);
  const snap = (s.activeSnapshotTs as string | null) ?? null;
  if (snap) {
    await enqueueShipment(snap);
    await chrome.storage.local.remove('activeSnapshotTs').catch(() => {});
  }
  await shipAndBroadcast();
}

/** Push every pending snapshot, broadcasting a SHIPMENT message per result so the dashboard can
 *  surface "снимок отправлен" / retry status. shipPending already removes successes + skips from
 *  the queue; this only adds the broadcast + console logging on top. */
async function shipAndBroadcast(): Promise<void> {
  const results = await shipPending();
  for (const r of results) {
    broadcastShipment(r);
  }
}

function broadcastShipment(r: PushResult): void {
  const status: ShipmentMsg['status'] = r.shipped ? 'shipped' : r.ok ? 'skipped' : 'failed';
  const msg: ShipmentMsg = { type: 'SHIPMENT', snapshot: r.snapshot, status };
  if (r.counts) msg.counts = r.counts;
  if (r.error) msg.error = r.error;
  if (status === 'shipped') {
    const total = r.counts ? Object.values(r.counts).reduce((a, b) => a + b, 0) : 0;
    console.log(`[Poncho] snapshot shipped: ${r.snapshot} (${total} rows)`);
  } else if (status === 'failed') {
    console.warn(`[Poncho] snapshot shipment failed (will retry): ${r.snapshot} — ${r.error}`);
  } // 'skipped' (no server URL) is silent — browser-only mode is the default
  void chrome.runtime.sendMessage(msg).catch(() => {});
}

/** On SW rebirth, recover any snapshot that finished but never shipped: an activeSnapshotTs that
 *  lingers while we are NOT actively collecting means the SW died in the narrow window before
 *  finishCollect enqueued it. Fold it into the pending queue, then push everything. */
async function sweepPendingShipments(): Promise<void> {
  const s = await chrome.storage.local.get(['mode', 'activeSnapshotTs']).catch(() => ({}) as Record<string, unknown>);
  const m = (s.mode as 'collect' | 'idle' | undefined) ?? 'idle';
  const active = (s.activeSnapshotTs as string | null) ?? null;
  if (active && m !== 'collect') {
    await enqueueShipment(active);
    await chrome.storage.local.remove('activeSnapshotTs').catch(() => {});
  }
  await shipAndBroadcast();
}

console.log('[Poncho] service worker started');
void sweepPendingShipments().catch((e) => console.warn('[Poncho] shipment sweep', e));
