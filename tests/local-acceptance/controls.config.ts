import base from './playwright.config';
import { defineConfig } from '@playwright/test';
import { resolve } from 'node:path';
export default defineConfig({ ...base, testMatch: 'controls.spec.ts', timeout: 180_000,
  outputDir: resolve(__dirname, '../../.artifacts/local-acceptance/full-controls/browser') });
