// inject.js — runs in the page's MAIN world (same context as WB's own JS).
// Wraps window.fetch and XMLHttpRequest to capture every request to *.wildberries.ru
// and forwards metadata (+ JSON body) to the ISOLATED content.js bridge via postMessage.
//
// It is intentionally mode-agnostic: it always emits everything to *.wb.ru.
// Filtering by mode (recon=keep all / collect=match patterns) happens in the SW.

(() => {
  'use strict';

  if (window.__WB_SCRAPER_INJECTED__) return; // guard against double-inject
  window.__WB_SCRAPER_INJECTED__ = true;

  const WB_HOST = /(^|\.)wildberries\.ru$/i;

  const isWbUrl = (u) => {
    try {
      return WB_HOST.test(new URL(u, location.href).hostname);
    } catch {
      return false;
    }
  };

  const emit = (payload) => {
    try {
      window.postMessage({ source: 'WB_SCRAPER', ts: Date.now(), ...payload }, '*');
    } catch {
      /* never let logging break the page */
    }
  };

  const parseBody = (text) => {
    try {
      return JSON.parse(text);
    } catch {
      return null; // not JSON — caller omits the body, keeps metadata only
    }
  };

  // WB serves some JSON under text/plain (e.g. search v18/exactmatch). Read the body
  // for any textual response and try to parse; skip obvious binaries.
  const looksTextual = (ct) => ct.includes('json') || ct.includes('text/') || ct === '';

  // ---------- fetch ----------
  const origFetch = window.fetch;
  window.fetch = async function (...args) {
    const resp = await origFetch.apply(this, args);
    try {
      const rawUrl = typeof args[0] === 'string' ? args[0] : args[0] && args[0].url;
      if (!rawUrl || !isWbUrl(rawUrl)) return resp;
      const ct = resp.headers.get('content-type') || '';
      const method = (args[1] && args[1].method) || (args[0] && args[0].method) || 'GET';
      let body, bytes = 0;
      if (looksTextual(ct)) {
        try {
          const text = await resp.clone().text(); // clone: don't consume the page's stream
          bytes = text.length;
          body = parseBody(text);
        } catch { /* ignore */ }
      }
      const rec = { via: 'fetch', method, url: rawUrl, status: resp.status, contentType: ct, bytes };
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

  XMLHttpRequest.prototype.open = function (method, url, ...rest) {
    this.__wb_method = method;
    this.__wb_url = url;
    return origOpen.call(this, method, url, ...rest);
  };

  XMLHttpRequest.prototype.send = function (body) {
    this.addEventListener('loadend', () => {
      try {
        const url = this.__wb_url;
        if (!url || !isWbUrl(url)) return;
        const ct = this.getResponseHeader('content-type') || '';
        let body, bytes = 0;
        if (looksTextual(ct)) {
          const text = this.responseText || '';
          bytes = text.length;
          body = parseBody(text);
        }
        const rec = { via: 'xhr', method: this.__wb_method, url, status: this.status, contentType: ct, bytes };
        if (body != null) rec.body = body;
        emit(rec);
      } catch {
        /* ignore */
      }
    });
    return origSend.call(this, body);
  };

  console.log('[WB Scraper] inject.js installed in MAIN world');
})();
