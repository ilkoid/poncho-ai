// vite.config.ts — build the MV3 extension via @crxjs/vite-plugin, plus vitest config.
//
// Why @crxjs is conditionally disabled for tests: vitest reuses vite's dev pipeline, and the
// crx() plugin rewires vite's input format around the manifest (HMR + content-script inputs),
// which breaks the test runner. process.env.VITEST is set by vitest, so we skip the plugin then.
// Precedent: the standard @crxjs+vitest recipe.
//
// defineConfig is imported from 'vitest/config' (not 'vite') so the `test` field is typed.

import { defineConfig } from 'vitest/config';
import { crx } from '@crxjs/vite-plugin';
import manifest from './manifest.config';

export default defineConfig({
  plugins: [
    // crx only during build/dev — never inside vitest (see file header).
    !process.env.VITEST && crx({ manifest }),
  ].filter(Boolean),
  build: {
    target: 'esnext',
    // xlsx is dynamically imported on export (S6) → vite auto-splits it into its own chunk,
    // keeping the initial SW/dashboard bundle small.
    //
    // sourcemap MUST be false: with it on, Vite appends `//# sourceMappingURL=…` to each content
    // script, and @crxjs places its IIFE-wrapper closing `})()` AFTER it on the same line — so the
    // closing is swallowed by the comment and the wrapper never closes → SyntaxError "Unexpected
    // end of input" → inject/bridge never run. v1 (plain JS, no build) never hit this. Verified
    // via F12 in Edge: injector.ts/bridge.ts failed to parse.
    sourcemap: false,
    // @crxjs auto-bundles only manifest-known HTML entries (popup/options/overrides). The dashboard
    // and offscreen HTML live in web_accessible_resources, which @crxjs copies verbatim WITHOUT
    // compiling their <script src="./x.ts">. Declaring them as rollup inputs makes Vite compile
    // their TS and rewrite the script tags to hashed chunks.
    rollupOptions: {
      input: {
        dashboard: 'src/dashboard/dashboard.html',
        offscreen: 'src/offscreen/offscreen.html',
      },
    },
  },
  test: {
    environment: 'node',
    setupFiles: ['./tests/setup.ts'],
    include: ['tests/**/*.test.ts'],
  },
});
