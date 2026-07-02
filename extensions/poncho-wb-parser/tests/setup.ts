// tests/setup.ts — polyfills for running the extension code under vitest (node):
//   - fake-indexeddb/auto: installs indexedDB + IDBKeyRange globals so Dexie works.
//   - chrome.storage.local mock: an in-memory Map, so config.ts / buildTargets are testable.

import 'fake-indexeddb/auto';

const memStore = new Map<string, unknown>();

const chromeMock = {
  storage: {
    local: {
      get: async (keys?: string | string[] | null | Record<string, unknown>): Promise<Record<string, unknown>> => {
        if (keys == null || (typeof keys === 'object' && !Array.isArray(keys) && Object.keys(keys).length === 0)) {
          return Object.fromEntries(memStore);
        }
        const arr = Array.isArray(keys) ? keys : typeof keys === 'string' ? [keys] : Object.keys(keys);
        const out: Record<string, unknown> = {};
        for (const k of arr) if (memStore.has(k)) out[k] = memStore.get(k);
        return out;
      },
      set: async (obj: Record<string, unknown>): Promise<void> => {
        for (const [k, v] of Object.entries(obj)) memStore.set(k, v);
      },
    },
  },
};

// Merge onto any existing chrome global (vitest/browser may provide one); else install ours.
globalThis.chrome = { ...(globalThis.chrome as object), ...chromeMock } as typeof chrome;

/** Test-only: wipe the chrome.storage.local mock + all Dexie stores between tests that need it. */
export function resetMockStorage(): void {
  memStore.clear();
}
