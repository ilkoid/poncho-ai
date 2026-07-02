// manifest.config.ts — MV3 manifest for Poncho WB Parser (compiled by @crxjs/vite-plugin).
// Ports v1 (extensions/wb-scraper/manifest.json) verbatim in content-script wiring:
// inject (MAIN-world fetch/XHR wrap) + bridge (ISOLATED relay), both document_start.
// NEW vs v1: dashboard.html (full-page 3-tab UI) + offscreen.html (run-loop owner).

import { defineManifest } from '@crxjs/vite-plugin';

const wbMatch = '*://*.wildberries.ru/*';

export default defineManifest({
  manifest_version: 3,
  name: 'Poncho WB Parser',
  version: '0.1.0',
  description: 'Browser-only WB storefront parser (visibility, competitor map, prices & stocks). v2 of wb-scraper — no Go backend.',
  permissions: ['tabs', 'scripting', 'storage', 'downloads', 'offscreen', 'activeTab'],
  host_permissions: [wbMatch],
  background: {
    service_worker: 'src/background/sw.ts',
    type: 'module',
  },
  action: {
    default_popup: 'src/popup/popup.html',
    default_title: 'Poncho WB Parser',
  },
  content_scripts: [
    {
      matches: [wbMatch],
      js: ['src/inject/injector.ts'],
      run_at: 'document_start',
      world: 'MAIN',
      all_frames: false,
    },
    {
      matches: [wbMatch],
      js: ['src/content/bridge.ts'],
      run_at: 'document_start',
      world: 'ISOLATED',
      all_frames: false,
    },
  ],
  web_accessible_resources: [
    {
      // dashboard.html opens as a full tab (chrome.tabs.create); offscreen.html hosts the run-loop.
      // (injector is injected via content_scripts, so it is not listed here.)
      resources: ['src/dashboard/dashboard.html', 'src/offscreen/offscreen.html'],
      matches: [wbMatch],
    },
  ],
});
