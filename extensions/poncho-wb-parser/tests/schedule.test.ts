// tests/schedule.test.ts — pure helpers of the chrome.alarms schedule (Этап D).
// minutesUntil accepts a fixed `now` so the time-math is deterministic; parseScheduleTimes is
// fully pure. rebuildSchedule (chrome.alarms side effects) is not unit-tested here — it's thin
// glue over the tested helpers + chrome.alarms, exercised live.

import { describe, it, expect } from 'vitest';
import { minutesUntil, parseScheduleTimes, isScheduledAlarm, SCHEDULED_ALARM_PREFIX } from '../src/background/schedule';

describe('parseScheduleTimes', () => {
  it('normalizes H:MM → HH:MM and keeps valid times in first-seen order', () => {
    expect(parseScheduleTimes('11:00\n17:00\n21:00')).toEqual(['11:00', '17:00', '21:00']);
    expect(parseScheduleTimes('9:00')).toEqual(['09:00']); // zero-pad hour
  });

  it('dedups after normalization (9:00 and 09:00 are the same alarm)', () => {
    expect(parseScheduleTimes('9:00\n09:00\n09:00')).toEqual(['09:00']);
  });

  it('drops malformed and out-of-range times, keeps the valid ones', () => {
    expect(parseScheduleTimes('25:00\nbad\n12:60\n10:30\n:30\n7')).toEqual(['10:30']);
  });

  it('tolerates comma + extra whitespace, returns [] for empty', () => {
    expect(parseScheduleTimes('  11:00 , 17:00  \n  21:00 ')).toEqual(['11:00', '17:00', '21:00']);
    expect(parseScheduleTimes('')).toEqual([]);
    expect(parseScheduleTimes('   \n   ')).toEqual([]);
  });
});

describe('minutesUntil', () => {
  // Fixed "now" = local 10:00:00 so the relative math is stable regardless of when the test runs.
  const now = new Date(2026, 6, 9, 10, 0, 0); // 2026-07-09 10:00 local

  it('returns the minutes to a later time today', () => {
    expect(minutesUntil('11:00', now)).toBe(60);
    expect(minutesUntil('10:30', now)).toBe(30);
  });

  it('rolls to tomorrow for a time already passed (or exactly now)', () => {
    expect(minutesUntil('09:00', now)).toBe(23 * 60); // yesterday's 09:00 passed → tomorrow 09:00
    expect(minutesUntil('10:00', now)).toBe(24 * 60); // exactly now → next is tomorrow 10:00
  });

  it('returns null for malformed / out-of-range', () => {
    expect(minutesUntil('bad', now)).toBeNull();
    expect(minutesUntil('24:00', now)).toBeNull();
    expect(minutesUntil('10:99', now)).toBeNull();
  });
});

describe('isScheduledAlarm', () => {
  it('recognizes the schedule prefix and rejects foreign alarm names', () => {
    expect(isScheduledAlarm(SCHEDULED_ALARM_PREFIX + '11:00')).toBe(true);
    expect(isScheduledAlarm('some-other-alarm')).toBe(false);
    expect(isScheduledAlarm('')).toBe(false);
  });
});
