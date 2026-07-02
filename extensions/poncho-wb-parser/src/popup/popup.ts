// src/popup/popup.ts — thin launcher. The real UI is the full-page dashboard (3 tabs); the popup
// only opens it and shows a one-line status so the user sees collected counts at a glance.
//
// Counts are read DIRECTLY from Dexie (the popup is an extension page with IndexedDB access),
// NOT via a GET_STATE message to the SW: the popup is ephemeral (closes on blur) and would often
// close before the SW's async reply → "message channel closed before a response was received"
// warning. Reading Dexie directly removes the async round-trip → no race.

import { db } from '../db/dexie';

async function renderCounts(): Promise<void> {
  const el = document.getElementById('status');
  if (!el) return;
  const counts: number[] = await Promise.all([
    db.search_positions.count(),
    db.vitrine_ads.count(),
    db.competitor_cards.count(),
    db.competitor_card_prices.count(),
    db.competitor_card_details.count(),
    db.competitor_card_stocks.count(),
  ]).catch(() => [0, 0, 0, 0, 0, 0]);
  const total = counts.reduce((n, c) => n + c, 0);
  const sp = counts[0] ?? 0; // search_positions
  const cc = counts[2] ?? 0; // competitor_cards
  el.textContent =
    total === 0 ? 'данных пока нет — откройте панель' : `в базе: ${total} строк\n(карточек: ${cc}, позиций: ${sp})`;
}

document.getElementById('open-dashboard')?.addEventListener('click', () => {
  void chrome.runtime.sendMessage({ type: 'OPEN_DASHBOARD' }).catch(() => {});
});

void renderCounts();
