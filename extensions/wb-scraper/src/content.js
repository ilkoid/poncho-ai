// content.js — runs in the ISOLATED world: has chrome.runtime, but NOT the page's window.
// Acts as a bridge: MAIN-world inject.js -> window.postMessage -> here -> SW via runtime.

(() => {
  'use strict';

  window.addEventListener('message', (e) => {
    // only same-window messages from our own injector
    if (e.source !== window) return;
    const d = e.data;
    if (!d || d.source !== 'WB_SCRAPER') return;
    // After an extension reload, this page still runs the OLD content script whose
    // extension context is invalidated (chrome.runtime.id becomes undefined). Bail out
    // silently instead of throwing "Extension context invalidated" into the page console.
    if (!chrome.runtime?.id) return;
    try {
      chrome.runtime.sendMessage({ type: 'INTERCEPT', payload: d }).catch(() => {});
    } catch { /* context invalidated — ignore */ }
  });

  // Collect loop asks the page to scroll down — triggers WB's lazy loader (SPA fetches
  // the next /search? page), which inject intercepts. Returns `grew` = whether the page
  // height increased (new content loaded). When it stops growing, the results are exhausted
  // and the loop stops scrolling that target. Gradual scroll + wait also fixes page 3+.
  chrome.runtime.onMessage.addListener((msg, sender, reply) => {
    if (msg && msg.type === 'SCROLL') {
      scrollDown()
        .then((grew) => reply({ ok: true, grew }))
        .catch(() => reply({ ok: false, grew: false }));
      return true; // async reply
    }
  });

  async function scrollDown() {
    const before = document.body.scrollHeight;
    const step = Math.floor(window.innerHeight * 0.8);
    for (let i = 0; i < 8; i++) {
      window.scrollBy(0, step);
      await new Promise((r) => setTimeout(r, 150));
    }
    window.scrollTo(0, document.body.scrollHeight);
    // give WB's lazy loader time to fire + render the next page
    await new Promise((r) => setTimeout(r, 1500));
    const after = document.body.scrollHeight;
    return after > before + 50; // grew = new content loaded (false = end of results)
  }

  console.log('[WB Scraper] content.js bridge ready (ISOLATED)');
})();
