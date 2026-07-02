// src/content/bridge.ts — runs in the ISOLATED world: has chrome.runtime, but NOT the page's window.
// Port of extensions/wb-scraper/src/content.js. The one context that can talk to BOTH the MAIN
// world (window events) AND the extension (chrome.runtime):
//   MAIN inject ──postMessage('PONCHO_INJECT')──► here ──INTERCEPT──► SW ──CAPTURE──► offscreen
//   offscreen ──SCROLL──► SW ──► here ──scrollBy──► page (triggers WB lazy /search loaders)

interface InjectMessage {
  source: 'PONCHO_INJECT';
  via: 'fetch' | 'xhr';
  method: string;
  url: string;
  status: number;
  contentType: string;
  bytes: number;
  body?: unknown;
}

window.addEventListener('message', (e: MessageEvent) => {
  if (e.source !== window) return;
  const d = e.data as Partial<InjectMessage> | null;
  if (!d || d.source !== 'PONCHO_INJECT') return;
  // After an extension reload, this page still runs the OLD content script whose extension context
  // is invalidated (chrome.runtime.id becomes undefined). Bail out silently instead of throwing
  // "Extension context invalidated" into the page console.
  if (!chrome.runtime?.id) return;
  try {
    void chrome.runtime.sendMessage({
      type: 'INTERCEPT',
      payload: {
        url: d.url,
        status: d.status,
        body: d.body ?? null,
      },
    }).catch(() => {});
  } catch {
    /* context invalidated — ignore */
  }
});

// The collect loop asks the page to scroll down — triggers WB's SPA lazy loader (it fetches the
// next /search? page), which inject intercepts. Returns `grew` = whether the page height increased
// (new content loaded). When it stops growing, results are exhausted and the loop stops scrolling.
chrome.runtime.onMessage.addListener((msg: unknown, _sender, reply: (r: unknown) => void) => {
  if (msg && typeof msg === 'object' && (msg as { type?: string }).type === 'SCROLL') {
    scrollDown()
      .then((grew) => reply({ ok: true, grew }))
      .catch(() => reply({ ok: false, grew: false }));
    return true; // async reply
  }
  return undefined;
});

async function scrollDown(): Promise<boolean> {
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

console.log('[Poncho] ISOLATED bridge ready');
