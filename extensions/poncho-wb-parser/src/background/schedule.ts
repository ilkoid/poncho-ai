// src/background/schedule.ts — chrome.alarms-based daily collect schedule (Этап D).
//
// One alarm per "HH:MM" time-of-day, created with delayInMinutes = minutes until the next occurrence
// and periodInMinutes = 24h, so each fires daily. The alarms are browser-managed and survive SW
// death; chrome.alarms.onAlarm (a wakeable event) restarts the SW to run startCollect. The pure
// helpers (minutesUntil / parseScheduleTimes) are exported for unit testing without Chrome.

import { loadScheduleTimes } from '../storage/config';

/** Alarm-name prefix for scheduled collects. onAlarm filters on this to distinguish schedule
 *  alarms from any other alarm the extension might create. The suffix is the "HH:MM" time. */
export const SCHEDULED_ALARM_PREFIX = 'poncho-schedule-';

const RE_HHMM = /^\s*(\d{1,2}):(\d{2})\s*$/;
const DAY_MS = 24 * 60 * 60 * 1000;

/** Normalize a raw "H:MM" / "HH:MM" to "HH:MM" (zero-padded hour). Returns null if malformed. */
function normalizeHHMM(s: string): string | null {
  const m = RE_HHMM.exec(s);
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  if (h > 23 || min > 59) return null;
  return `${String(h).padStart(2, '0')}:${m[2]}`;
}

/** Minutes from now until the next occurrence of "HH:MM" (today if still ahead, else tomorrow).
 *  Returns null for a malformed time. Pure except for reading the clock (now). */
export function minutesUntil(hhmm: string, now: Date = new Date()): number | null {
  const norm = normalizeHHMM(hhmm);
  if (!norm) return null;
  const [h, min] = norm.split(':').map(Number);
  const target = new Date(now);
  target.setHours(h!, min!, 0, 0);
  let delayMs = target.getTime() - now.getTime();
  if (delayMs <= 0) delayMs += DAY_MS; // already passed today → same time tomorrow
  return delayMs / 60000;
}

/** Parse a raw multiline / comma-separated string into validated, de-duplicated, normalized "HH:MM"
 *  times, preserving first-seen order. Empty/malformed entries are dropped. Pure. */
export function parseScheduleTimes(raw: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const tok of raw.split(/[\s,]+/)) {
    if (!tok) continue;
    const norm = normalizeHHMM(tok);
    if (!norm || seen.has(norm)) continue;
    seen.add(norm);
    out.push(norm);
  }
  return out;
}

/** Recreate the daily alarms from the saved schedule: one alarm per "HH:MM". Clears every prior
 *  schedule alarm first, so the live set always matches the config (idempotent). No-op when the
 *  schedule is empty (all alarms cleared → no scheduled collects). */
export async function rebuildSchedule(): Promise<void> {
  const times = await loadScheduleTimes();
  for (const a of await chrome.alarms.getAll()) {
    if (a.name.startsWith(SCHEDULED_ALARM_PREFIX)) {
      await chrome.alarms.clear(a.name).catch(() => {});
    }
  }
  for (const t of times) {
    const delay = minutesUntil(t);
    if (delay == null) continue;
    await chrome.alarms
      .create(SCHEDULED_ALARM_PREFIX + t, { delayInMinutes: delay, periodInMinutes: 24 * 60 })
      .catch((e) => console.warn('[Poncho] alarm create', t, e));
  }
  console.log(`[Poncho] schedule rebuilt: ${times.length} alarm(s) — ${times.join(', ') || '(empty, manual only)'}`);
}

/** True iff an alarm name is one of ours (the onAlarm handler uses this to ignore foreign alarms). */
export function isScheduledAlarm(name: string): boolean {
  return name.startsWith(SCHEDULED_ALARM_PREFIX);
}
