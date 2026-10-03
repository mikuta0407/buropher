import { defineConfig } from '@playwright/test';
import * as path from 'node:path';
import * as fs from 'node:fs';

// Browsers live under <repo>/_reference/playwright-browsers (not /tmp, which is a small tmpfs).
// Walk up from this file so it also works from a git worktree.
if (!process.env.PLAYWRIGHT_BROWSERS_PATH) {
  let d = __dirname;
  while (d !== path.dirname(d)) {
    if (fs.existsSync(path.join(d, '_reference'))) {
      process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(d, '_reference', 'playwright-browsers');
      break;
    }
    d = path.dirname(d);
  }
}

export default defineConfig({
  testDir: './specs',
  outputDir: './test-results/artifacts',
  // Both servers share one database each and specs reset them, so everything runs serially.
  workers: 1,
  fullyParallel: false,
  // A spec resets two servers (~25s) and drives the scenario twice.
  timeout: 240_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['json', { outputFile: 'test-results/report.json' }]],
  use: {
    headless: true,
    locale: 'en-US',
    timezoneId: 'UTC',
    viewport: { width: 1280, height: 900 },
    actionTimeout: 10_000,
    navigationTimeout: 30_000,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'behavior', testMatch: /behavior\/.*\.spec\.ts/ },
    { name: 'visual', testMatch: /visual\/.*\.spec\.ts/ },
  ],
});
