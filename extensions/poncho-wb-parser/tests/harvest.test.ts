// tests/harvest.test.ts — pure unit tests for the detail-harvest helpers (no chrome, no DB).
// pollUntilNonEmpty is the fix for the race where pickHarvestNmids read search_positions before
// the async /search capture had landed (few results → scroll broke early → empty one-shot read).

import { describe, it, expect } from 'vitest';
import { pollUntilNonEmpty, pickUnharvested } from '../src/offscreen/harvest';

const noSnooze = async (): Promise<void> => {}; // instant — tests stay fast

describe('pollUntilNonEmpty', () => {
  it('returns as soon as read() is non-empty (no extra polls)', async () => {
    let calls = 0;
    const read = async () => {
      calls++;
      return calls >= 3 ? ([{ nm_id: 1 }, { nm_id: 2 }] as { nm_id: number }[]) : [];
    };
    const out = await pollUntilNonEmpty(read, () => false, noSnooze, 1, 10);
    expect(calls).toBe(3);
    expect(out).toEqual([{ nm_id: 1 }, { nm_id: 2 }]);
  });

  it('returns [] after maxPolls when read() stays empty', async () => {
    let calls = 0;
    const read = async () => {
      calls++;
      return [];
    };
    const out = await pollUntilNonEmpty(read, () => false, noSnooze, 1, 4);
    expect(calls).toBe(4); // exactly maxPolls attempts
    expect(out).toEqual([]);
  });

  it('stops early when shouldStop() flips true', async () => {
    let calls = 0;
    let stopped = false;
    const read = async () => {
      calls++;
      return [];
    };
    // flip stopped during the snooze between attempt 1 and 2
    const snooze = async () => {
      stopped = true;
    };
    await pollUntilNonEmpty(read, () => stopped, snooze, 1, 10);
    expect(calls).toBeLessThan(10);
    expect(calls).toBe(2); // 1st read (empty) → snooze sets stopped → 2nd read → bail
  });

  it('does not snooze after a successful read', async () => {
    let snoozes = 0;
    const read = async () => [{ nm_id: 1 }];
    await pollUntilNonEmpty(read, () => false, async () => { snoozes++; }, 1, 5);
    expect(snoozes).toBe(0);
  });

  it('does not snooze after the final maxPolls attempt', async () => {
    let snoozes = 0;
    const read = async () => [];
    await pollUntilNonEmpty(read, () => false, async () => { snoozes++; }, 1, 3);
    expect(snoozes).toBe(2); // snooze between 1↔2 and 2↔3, but NOT after attempt 3
  });

  it('succeeds on the very first read', async () => {
    let calls = 0;
    const read = async () => {
      calls++;
      return [{ nm_id: 7 }];
    };
    const out = await pollUntilNonEmpty(read, () => false, noSnooze, 1, 5);
    expect(calls).toBe(1);
    expect(out).toEqual([{ nm_id: 7 }]);
  });
});

describe('pickUnharvested', () => {
  it('skips already-harvested nm_ids and caps at limit, recording picks', () => {
    const harvested = new Set<number>([2]);
    const out = pickUnharvested([{ nm_id: 1 }, { nm_id: 2 }, { nm_id: 3 }, { nm_id: 4 }], harvested, 2);
    expect(out).toEqual([1, 3]); // 2 skipped (already harvested), stopped at limit 2
    expect(harvested.has(1)).toBe(true);
    expect(harvested.has(3)).toBe(true);
    expect(harvested.size).toBe(3); // {2,1,3}
  });

  it('returns fewer than limit when rows run out', () => {
    const out = pickUnharvested([{ nm_id: 5 }], new Set(), 8);
    expect(out).toEqual([5]);
  });

  it('returns [] when all rows are already harvested', () => {
    const harvested = new Set<number>([10, 11]);
    const out = pickUnharvested([{ nm_id: 10 }, { nm_id: 11 }], harvested, 5);
    expect(out).toEqual([]);
  });
});
