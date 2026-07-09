// src/inject/injector.ts — runs in the page's MAIN world (same context as WB's own JS).
// Port of extensions/wb-scraper/src/inject.js: wraps window.fetch + XMLHttpRequest to capture every
// request to *.wildberries.ru (storefront APIs) and *.wbbasket.ru (the card.json content CDN) and
// forward metadata (+ parsed JSON body) to the ISOLATED bridge via
// postMessage. Mode-agnostic: it emits everything; the SW classifies (recon=keep all / collect=
// match COLLECT_PATTERNS). Marker is 'PONCHO_INJECT' (distinct from v1's 'WB_SCRAPER') so the two
// extensions don't cross-capture when both are loaded.

(() => {
  'use strict';

  // Guard against double-inject (content script can re-fire on SPA route changes within the tab).
  const FLAG = '__PONCHO_WB_PARSER_INJECTED__' as const;
  type WindowWithFlag = Window & { [FLAG]?: boolean };
  if ((window as WindowWithFlag)[FLAG]) return;
  (window as WindowWithFlag)[FLAG] = true;

  // Hosts we capture: *.wildberries.ru (storefront APIs) AND *.wbbasket.ru (the static CDN that
  // serves card.json — product description/characteristics — at /vol{a}/part{b}/{nmId}/info/ru/card.json).
  const WB_HOST = /(^|\.)(wildberries|wbbasket)\.ru$/i;

  const isWbUrl = (u: string): boolean => {
    try {
      return WB_HOST.test(new URL(u, location.href).hostname);
    } catch {
      return false;
    }
  };

  interface CaptureRecord {
    via: 'fetch' | 'xhr';
    method: string;
    url: string;
    status: number;
    contentType: string;
    bytes: number;
    body?: unknown;
  }

  const emit = (rec: CaptureRecord): void => {
    try {
      window.postMessage({ source: 'PONCHO_INJECT', ts: Date.now(), ...rec }, '*');
    } catch {
      /* never let logging break the page */
    }
  };

  const parseBody = (text: string): unknown => {
    try {
      return JSON.parse(text);
    } catch {
      return null; // not JSON → caller omits body, keeps metadata only
    }
  };

  // WB serves some JSON under text/plain (e.g. search v18/exactmatch). Read the body for any textual
  // response and try to parse; skip obvious binaries.
  const looksTextual = (ct: string): boolean => ct.includes('json') || ct.includes('text/') || ct === '';

  // ---------- fetch ----------
  const origFetch = window.fetch;
  window.fetch = async function (this: unknown, ...args: Parameters<typeof origFetch>): Promise<Response> {
    const resp = await origFetch.apply(this as unknown as typeof window, args);
    try {
      const input = args[0];
      const init = args[1];
      let rawUrl: string | undefined;
      if (typeof input === 'string') rawUrl = input;
      else if (input instanceof URL) rawUrl = input.href;
      else rawUrl = input.url; // Request
      if (!rawUrl || !isWbUrl(rawUrl)) return resp;
      const ct = resp.headers.get('content-type') || '';
      const method: string = init?.method ?? (input instanceof Request ? input.method : 'GET');
      let body: unknown = null;
      let bytes = 0;
      if (looksTextual(ct)) {
        try {
          const text = await resp.clone().text(); // clone: don't consume the page's stream
          bytes = text.length;
          body = parseBody(text);
        } catch {
          /* ignore */
        }
      }
      const rec: CaptureRecord = { via: 'fetch', method, url: rawUrl, status: resp.status, contentType: ct, bytes };
      if (body != null) rec.body = body; // null = not JSON / binary → metadata only
      emit(rec);
    } catch {
      /* ignore */
    }
    return resp;
  };

  // ---------- XMLHttpRequest ----------
  const origOpen = XMLHttpRequest.prototype.open;
  const origSend = XMLHttpRequest.prototype.send;
  type XHRWithMarks = XMLHttpRequest & { __poncho_method?: string; __poncho_url?: string };

  XMLHttpRequest.prototype.open = function (
    this: XHRWithMarks,
    method: string,
    url: string | URL,
    async: boolean = true,
    username?: string | null,
    password?: string | null,
  ): void {
    this.__poncho_method = method;
    this.__poncho_url = String(url);
    return origOpen.call(this as XMLHttpRequest, method, url, async, username ?? null, password ?? null);
  };

  XMLHttpRequest.prototype.send = function (this: XHRWithMarks, body: Document | XMLHttpRequestBodyInit | null): void {
    this.addEventListener('loadend', () => {
      try {
        const url = this.__poncho_url;
        if (!url || !isWbUrl(url)) return;
        const ct = this.getResponseHeader('content-type') || '';
        let parsed: unknown = null;
        let bytes = 0;
        if (looksTextual(ct)) {
          const text = this.responseText || '';
          bytes = text.length;
          parsed = parseBody(text);
        }
        const rec: CaptureRecord = {
          via: 'xhr',
          method: this.__poncho_method || 'GET',
          url,
          status: this.status,
          contentType: ct,
          bytes,
        };
        if (parsed != null) rec.body = parsed;
        emit(rec);
      } catch {
        /* ignore */
      }
    });
    return origSend.call(this as XMLHttpRequest, body);
  };

  console.log('[Poncho] MAIN-world injector installed');
})();
