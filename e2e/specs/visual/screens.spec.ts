// Visual comparison: full-page screenshots of key pages from the reference Redmine and from
// buropher, at desktop (1280px) and mobile (375px) widths, compared with pixelmatch.
// Diff images go to test-results/visual/; a summary table to test-results/visual/summary.md.
// Pages whose differing-pixel ratio exceeds the threshold are reported as soft failures.
import { test, expect, Browser } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { PNG } from 'pngjs';
import pixelmatch from 'pixelmatch';
import { ref, cand, Target } from '../../lib/targets';

const OUT = path.join(__dirname, '..', '..', 'test-results', 'visual');
// Ratio of differing pixels (after pixelmatch's anti-aliasing detection) tolerated per page.
const THRESHOLD = Number(process.env.E2E_VISUAL_THRESHOLD ?? '0.005');

interface PageDef {
  name: string;
  url: string;
  user?: 'admin' | 'anonymous';
}

const PAGES: PageDef[] = [
  { name: 'login', url: '/login', user: 'anonymous' },
  { name: 'welcome-anonymous', url: '/', user: 'anonymous' },
  { name: 'welcome', url: '/' },
  { name: 'projects', url: '/projects' },
  { name: 'project-overview', url: '/projects/ecookbook' },
  { name: 'activity', url: '/projects/ecookbook/activity' },
  { name: 'roadmap', url: '/projects/ecookbook/roadmap' },
  { name: 'version', url: '/versions/2' },
  { name: 'issues', url: '/projects/ecookbook/issues' },
  { name: 'issues-grouped', url: '/projects/ecookbook/issues?set_filter=1&group_by=status&t[]=estimated_hours&c[]=tracker&c[]=subject&c[]=estimated_hours' },
  { name: 'issue', url: '/issues/1' },
  { name: 'issue-with-history', url: '/issues/2' },
  { name: 'issue-new', url: '/projects/ecookbook/issues/new' },
  { name: 'issue-bulk-edit', url: '/issues/bulk_edit?ids[]=1&ids[]=2' },
  { name: 'gantt', url: '/projects/ecookbook/issues/gantt?month=1&year=2026' },
  { name: 'calendar', url: '/projects/ecookbook/issues/calendar?month=1&year=2026' },
  { name: 'news', url: '/projects/ecookbook/news' },
  { name: 'news-item', url: '/news/1' },
  { name: 'documents', url: '/projects/ecookbook/documents' },
  { name: 'wiki', url: '/projects/ecookbook/wiki' },
  { name: 'wiki-index', url: '/projects/ecookbook/wiki/index' },
  { name: 'wiki-edit', url: '/projects/ecookbook/wiki/CookBook_documentation/edit' },
  { name: 'forum', url: '/projects/ecookbook/boards/1' },
  { name: 'forum-topic', url: '/boards/1/topics/1' },
  { name: 'files', url: '/projects/ecookbook/files' },
  { name: 'time-entries', url: '/projects/ecookbook/time_entries' },
  { name: 'time-report', url: '/projects/ecookbook/time_entries/report?criteria[]=user&criteria[]=activity&columns=month' },
  { name: 'project-settings', url: '/projects/ecookbook/settings' },
  { name: 'project-settings-members', url: '/projects/ecookbook/settings/members' },
  { name: 'search', url: '/search?q=recipe' },
  { name: 'my-page', url: '/my/page' },
  { name: 'my-account', url: '/my/account' },
  { name: 'admin', url: '/admin' },
  { name: 'admin-users', url: '/users' },
  { name: 'admin-user-edit', url: '/users/2/edit' },
  { name: 'admin-trackers', url: '/trackers' },
  { name: 'admin-workflows', url: '/workflows/edit?role_id=1&tracker_id=1' },
  { name: 'admin-settings', url: '/settings' },
  { name: 'admin-custom-fields', url: '/custom_fields' },
];

const VIEWPORTS = [
  { label: 'desktop', width: 1280, height: 900, isMobile: false },
  { label: 'mobile', width: 375, height: 812, isMobile: true },
] as const;

interface Row {
  page: string;
  viewport: string;
  ratio: number;
  refSize: string;
  candSize: string;
  over: boolean;
}

const rows: Row[] = [];

// login signs in through the form in this context. Each screenshot gets its own session:
// buropher keeps sessions server side, so a shared cookie would carry state (e.g. the issue
// query used for the "« Previous | Next »" links) from one page to the next, unlike Redmine's
// cookie store.
async function login(page: import('@playwright/test').Page, user: string) {
  await page.goto('/login');
  await page.fill('#username', user);
  await page.fill('#password', user);
  await Promise.all([page.waitForNavigation(), page.click('#login-submit')]);
}

async function shoot(browser: Browser, target: Target, def: PageDef, vp: (typeof VIEWPORTS)[number]): Promise<Buffer> {
  const ctx = await browser.newContext({
    baseURL: target.baseURL,
    viewport: { width: vp.width, height: vp.height },
    isMobile: vp.isMobile,
    hasTouch: vp.isMobile,
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'UTC',
  });
  const page = await ctx.newPage();
  const user = def.user ?? 'admin';
  if (user !== 'anonymous') await login(page, user);
  // Same browser clock on both sides (relative dates computed client side, datepickers).
  await page.clock.setFixedTime(new Date('2026-01-15T12:00:00Z'));
  await page.goto(def.url, { waitUntil: 'load' });
  await page.evaluate(() => document.fonts.ready);
  // Focus/caret and hover state must not leak into the picture.
  await page.mouse.move(0, 0);
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  const buf = await page.screenshot({ fullPage: true, animations: 'disabled', caret: 'hide' });
  await ctx.close();
  return buf;
}

function pad(img: PNG, w: number, h: number): PNG {
  if (img.width === w && img.height === h) return img;
  const out = new PNG({ width: w, height: h });
  out.data.fill(255);
  PNG.bitblt(img, out, 0, 0, img.width, img.height, 0, 0);
  return out;
}

test.beforeAll(() => {
  fs.rmSync(OUT, { recursive: true, force: true });
  fs.mkdirSync(OUT, { recursive: true });
  // Screenshots do not modify data, but start both sides from the pristine fixtures.
  if (process.env.E2E_NO_RESET !== '1') {
    ref.reset();
    cand.reset();
  }
});

test.afterAll(() => {
  const lines = [
    `# Visual comparison (threshold ${(THRESHOLD * 100).toFixed(2)}% differing pixels)`,
    '',
    '| page | viewport | diff % | ref size | buropher size | |',
    '|---|---|---:|---|---|---|',
    ...rows
      .sort((a, b) => b.ratio - a.ratio)
      .map((r) => `| ${r.page} | ${r.viewport} | ${(r.ratio * 100).toFixed(3)} | ${r.refSize} | ${r.candSize} | ${r.over ? 'OVER' : ''} |`),
  ];
  fs.writeFileSync(path.join(OUT, 'summary.md'), lines.join('\n') + '\n');
  fs.writeFileSync(path.join(OUT, 'summary.json'), JSON.stringify(rows, null, 2));
});

for (const def of PAGES) {
  for (const vp of VIEWPORTS) {
    test(`visual: ${def.name} (${vp.label})`, async ({ browser }) => {
      const a = PNG.sync.read(await shoot(browser, ref, def, vp));
      const b = PNG.sync.read(await shoot(browser, cand, def, vp));
      const w = Math.max(a.width, b.width);
      const h = Math.max(a.height, b.height);
      const pa = pad(a, w, h);
      const pb = pad(b, w, h);
      const diff = new PNG({ width: w, height: h });
      const n = pixelmatch(pa.data, pb.data, diff.data, w, h, { threshold: 0.1, includeAA: false, alpha: 0.2 });
      const ratio = n / (w * h);
      const base = path.join(OUT, `${def.name}-${vp.label}`);
      const over = ratio > THRESHOLD || a.height !== b.height;
      // E2E_VISUAL_SAVE_ALL=1 keeps the screenshots of every page (not only the differing ones).
      if (over || process.env.E2E_VISUAL_SAVE_ALL === '1') {
        fs.writeFileSync(`${base}-ref.png`, PNG.sync.write(pa));
        fs.writeFileSync(`${base}-cand.png`, PNG.sync.write(pb));
        fs.writeFileSync(`${base}-diff.png`, PNG.sync.write(diff));
      }
      rows.push({
        page: def.name,
        viewport: vp.label,
        ratio,
        refSize: `${a.width}x${a.height}`,
        candSize: `${b.width}x${b.height}`,
        over,
      });
      // Individual pages only record (a failing test would restart the worker and lose the table);
      // the summary test below fails when any page is over the threshold.
      if (over) test.info().annotations.push({ type: 'visual-diff', description: `${(ratio * 100).toFixed(3)}% ${base}-diff.png` });
    });
  }
}

test('visual: summary (pages over threshold)', () => {
  const over = rows.filter((r) => r.over).map((r) => `${r.page} (${r.viewport}): ${(r.ratio * 100).toFixed(3)}% ref ${r.refSize} cand ${r.candSize}`);
  expect(over, 'pages whose screenshots differ; see test-results/visual/').toEqual([]);
});
