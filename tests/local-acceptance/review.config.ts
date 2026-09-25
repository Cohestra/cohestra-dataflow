import { defineConfig } from '@playwright/test';
import { mkdirSync, chmodSync } from 'node:fs';
import { resolve } from 'node:path';

const artifacts = resolve(__dirname, '../../.artifacts/local-acceptance/full-review');
mkdirSync(artifacts, { recursive: true, mode: 0o700 });
chmodSync(artifacts, 0o700);
export default defineConfig({
  testDir: __dirname,
  testMatch: 'review.spec.ts',
  outputDir: resolve(artifacts, 'browser-continuation'),
  workers: 1,
  retries: 0,
  timeout: 360_000,
  expect: { timeout: 20_000 },
  reporter: [['list'], ['json', { outputFile: resolve(artifacts, 'continuation-results.json') }]],
  use: {
    baseURL: 'http://localhost:13002', channel: 'chrome',
    viewport: { width: 1440, height: 1000 }, screenshot: 'only-on-failure', trace: 'off',
  },
});
