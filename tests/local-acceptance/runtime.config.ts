import { defineConfig } from '@playwright/test';
import { resolve } from 'node:path';
export default defineConfig({
  testDir: __dirname, testMatch: 'runtime.spec.ts', workers: 1, retries: 0,
  timeout: 120_000, expect: { timeout: 15_000 }, reporter: 'list',
  outputDir: resolve(__dirname, '../../.artifacts/local-acceptance/sept25-runtime-browser/results'),
  use: { baseURL: 'http://localhost:13002', channel: 'chrome', viewport: { width: 1440, height: 1000 }, screenshot: 'only-on-failure', trace: 'off' },
});
