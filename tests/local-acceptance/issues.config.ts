import { defineConfig } from '@playwright/test';
import { resolve } from 'node:path';

// Opt in with: npx playwright test -c tests/local-acceptance/issues.config.ts
// ACCEPTANCE_WEB_URL points at a second local stack when one is already running.
export default defineConfig({
  testDir: __dirname,
  testMatch: 'issues.spec.ts',
  outputDir: resolve(__dirname, '../../.artifacts/local-acceptance/issues'),
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: 'list',
  use: {
    baseURL: process.env.ACCEPTANCE_WEB_URL ?? 'http://localhost:13002',
    channel: 'chrome',
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    trace: 'off',
  },
});
