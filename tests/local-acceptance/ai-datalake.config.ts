import { defineConfig } from '@playwright/test';
import { resolve } from 'node:path';

// Browser-only (mocked API) AI builder regressions, including lakehouse pipelines; the API is mocked
// per test, so only the built web app is served. Build it first:
//   npm -w apps/web run build && npx playwright test -c tests/local-acceptance/ai-datalake.config.ts
// Set PLAYWRIGHT_CHROMIUM to use a preinstalled Chromium binary; set AI_LIVE_API to
// also run ai-datalake-live.spec.ts against a real planner + Ollama.
export default defineConfig({
  testDir: __dirname,
  testMatch: ['ai-datalake.spec.ts', 'review-keyboard.spec.ts', 'ai-datalake-live.spec.ts'],
  outputDir: resolve(__dirname, '../../.artifacts/local-acceptance/ai-datalake'),
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  webServer: {
    command: 'npx vite preview --port 13102 --strictPort',
    cwd: resolve(__dirname, '../../apps/web'),
    url: 'http://localhost:13102',
    reuseExistingServer: true,
  },
  use: {
    baseURL: 'http://localhost:13102',
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM } : {},
  },
});
