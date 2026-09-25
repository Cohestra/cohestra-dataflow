import { defineConfig } from '@playwright/test';
import original from '../../apps/web/playwright.config';
import { resolve } from 'node:path';

const { webServer: _server, ...base } = original;
const artifacts = resolve(__dirname, '../../.artifacts/local-acceptance/responsive');
const metadataTests = resolve(__dirname, '../../apps/web/tests');

export default defineConfig({
  ...base,
  workers: 1, retries: 0, timeout: 90_000,
  outputDir: resolve(artifacts, 'results'),
  reporter: [['list'], ['json', { outputFile: resolve(artifacts, 'results.json') }]],
  use: { ...base.use, baseURL: 'http://localhost:13002', screenshot: 'on', trace: 'retain-on-failure' },
  projects: [
    { name: 'desktop-metadata', testDir: metadataTests, testMatch: 'pipeline-metadata.spec.ts', use: { viewport: { width: 1440, height: 1000 } } },
    { name: 'mobile-metadata', testDir: metadataTests, testMatch: 'pipeline-metadata.spec.ts', use: { viewport: { width: 430, height: 932 } } },
    { name: 'mobile-keyboard', testDir: __dirname, testMatch: 'review-keyboard.spec.ts', use: { viewport: { width: 430, height: 932 } } },
  ],
});
