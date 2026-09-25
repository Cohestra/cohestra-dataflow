import { defineConfig } from '@playwright/test';
import { resolve } from 'node:path';

// Opt in with: npx playwright test -c tests/local-acceptance/playwright.config.ts
// Requires the isolated local runtime and the smoke script's generated fixture.
export default defineConfig({
  testDir: __dirname,
  testMatch: 'browser.spec.ts',
  outputDir: resolve(__dirname, '../../.artifacts/local-acceptance/browser'),
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: 'list',
  use: {
    baseURL: 'http://localhost:13002',
    channel: 'chrome',
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    // Start tracing after login so the password is never recorded in a trace.
    trace: 'off',
  },
});
