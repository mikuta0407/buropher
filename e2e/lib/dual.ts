// dualTest runs one scenario against the reference Redmine and against buropher and
// asserts that the recorded observations (DOM state, URLs, requests, dialogs, JS errors)
// are identical. The reference run must succeed on its own; a failure on the candidate
// side is recorded as an observation so the diff shows where the behavior diverged.
import { test, expect, Browser, BrowserContext, Page, Request } from '@playwright/test';
import { Target, targets } from './targets';
import * as fs from 'node:fs';
import * as path from 'node:path';

export type Obs = [string, unknown];

const VOLATILE_PARAMS = new Set(['authenticity_token', 'utf8', '_']);

export function normalizeURL(raw: string): string {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return raw;
  }
  const params = [...u.searchParams.entries()].filter(([k]) => !VOLATILE_PARAMS.has(k));
  const q = params.map(([k, v]) => `${k}=${v}`).join('&');
  return u.pathname + (q ? `?${q}` : '') + (u.hash || '');
}

function normalizeBody(req: Request): unknown {
  const ct = (req.headers()['content-type'] || '').split(';')[0];
  const body = req.postData();
  if (!body) return undefined;
  if (ct === 'application/x-www-form-urlencoded') {
    return new URLSearchParams(body)
      .toString()
      .split('&')
      .filter((kv) => !VOLATILE_PARAMS.has(decodeURIComponent(kv.split('=')[0])))
      .map((kv) => decodeURIComponent(kv.replace(/\+/g, ' ')));
  }
  if (ct === 'multipart/form-data') {
    // Only the field names (the boundary and file bytes are not interesting).
    const names = [...body.matchAll(/name="([^"]+)"/g)].map((m) => m[1]).filter((n) => !VOLATILE_PARAMS.has(n));
    return { multipart: names };
  }
  if (ct === 'application/octet-stream') return { octetStream: req.postDataBuffer()?.length };
  if (ct === 'application/json') return body;
  return { contentType: ct };
}

export const collapse = (s: string | null | undefined) => (s ?? '').replace(/\s+/g, ' ').trim();

export class Session {
  readonly obs: Obs[] = [];
  private reqs: string[] = [];
  readonly httpErrors: string[] = [];
  readonly jsErrors: string[] = [];
  readonly dialogs: string[] = [];
  /** dialogResponse decides how confirm()/beforeunload dialogs are answered. */
  dialogResponse: 'accept' | 'dismiss' = 'accept';
  private inflight = 0;
  private lastAjaxDone = 0;

  constructor(
    readonly target: Target,
    readonly context: BrowserContext,
    readonly page: Page,
  ) {
    page.on('request', (r) => {
      const t = r.resourceType();
      if (t !== 'document' && t !== 'xhr' && t !== 'fetch') return;
      if (r.isNavigationRequest() && r.frame() !== page.mainFrame()) return;
      const body = normalizeBody(r);
      this.reqs.push(`${r.method()} ${normalizeURL(r.url())}${body !== undefined ? ' ' + JSON.stringify(body) : ''}`);
    });
    const isAjax = (r: Request) => r.resourceType() === 'xhr' || r.resourceType() === 'fetch';
    page.on('request', (r) => {
      if (isAjax(r)) this.inflight++;
    });
    const done = (r: Request) => {
      if (isAjax(r)) {
        this.inflight = Math.max(0, this.inflight - 1);
        this.lastAjaxDone = Date.now();
      }
    };
    page.on('requestfinished', done);
    page.on('requestfailed', done);
    page.on('response', (r) => {
      if (r.status() >= 400) this.httpErrors.push(`${r.status()} ${r.request().method()} ${normalizeURL(r.url())}`);
    });
    page.on('pageerror', (e) => this.jsErrors.push(e.message));
    page.on('dialog', async (d) => {
      this.dialogs.push(`${d.type()}: ${d.message()}`);
      if (this.dialogResponse === 'accept') await d.accept();
      else await d.dismiss();
    });
  }

  note(key: string, value: unknown) {
    this.obs.push([key, value]);
  }

  /** Returns and clears the document/XHR requests seen since the previous call. */
  takeRequests(): string[] {
    const r = this.reqs;
    this.reqs = [];
    return r;
  }

  noteRequests(key: string) {
    this.note(`${key} requests`, this.takeRequests());
  }

  /**
   * Records only the last request since the previous call. For type-ahead searches whose
   * intermediate requests depend on typing speed versus response time (observeSearchfield).
   */
  noteLastRequest(key: string) {
    const r = this.takeRequests();
    this.note(`${key} last request`, r[r.length - 1] ?? null);
  }

  path(): string {
    return normalizeURL(this.page.url());
  }

  noteURL(key = 'url') {
    this.note(key, this.path());
  }

  async text(selector: string): Promise<string> {
    const loc = this.page.locator(selector);
    if ((await loc.count()) === 0) return '<missing>';
    return collapse(await loc.first().innerText());
  }

  async noteText(key: string, selector: string) {
    this.note(key, await this.text(selector));
  }

  /** Normalized texts of every element matching selector. */
  async texts(selector: string): Promise<string[]> {
    return (await this.page.locator(selector).allInnerTexts()).map(collapse);
  }

  async noteTexts(key: string, selector: string) {
    this.note(key, await this.texts(selector));
  }

  async visible(selector: string): Promise<boolean> {
    const loc = this.page.locator(selector);
    return (await loc.count()) > 0 && (await loc.first().isVisible());
  }

  async noteVisible(key: string, selector: string) {
    this.note(key, await this.visible(selector));
  }

  async value(selector: string): Promise<string> {
    return this.page.locator(selector).first().inputValue();
  }

  /** Runs an action that triggers a full page navigation and waits for the new page to load. */
  async nav(action: () => Promise<unknown>) {
    await Promise.all([this.page.waitForNavigation({ waitUntil: 'load' }), action()]);
  }

  async login(login: string, password: string) {
    await this.page.goto('/login');
    await this.page.fill('#username', login);
    await this.page.fill('#password', password);
    await Promise.all([this.page.waitForLoadState('load'), this.page.click('#login-submit')]);
    await this.page.waitForLoadState('load');
  }

  /**
   * Waits until no XHR/fetch is in flight (jQuery and rails-ujs both), the responses had time
   * to be evaluated, and any navigation they triggered has loaded.
   */
  async ajaxIdle(quietMs = 150) {
    const deadline = Date.now() + 15_000;
    // Let a just-triggered request start.
    await this.page.waitForTimeout(50);
    while (Date.now() < deadline) {
      if (this.inflight === 0 && Date.now() - this.lastAjaxDone >= quietMs) break;
      await this.page.waitForTimeout(25);
    }
    await this.page.waitForFunction(() => {
      const w = window as unknown as { jQuery?: { active: number } };
      return !w.jQuery || w.jQuery.active === 0;
    });
    await this.page.waitForLoadState('load');
  }
}

export type Scenario = (s: Session) => Promise<void>;

export interface DualOptions {
  /** Skip the database reset (the scenario does not modify data). */
  readOnly?: boolean;
  viewport?: { width: number; height: number };
  /** Extra context options (e.g. isMobile / hasTouch). */
  contextOptions?: Parameters<Browser['newContext']>[0];
}

async function runOn(browser: Browser, target: Target, scenario: Scenario, opts: DualOptions): Promise<Obs[]> {
  if (!opts.readOnly) target.reset();
  const context = await browser.newContext({
    baseURL: target.baseURL,
    locale: 'en-US',
    timezoneId: 'UTC',
    viewport: opts.viewport ?? { width: 1280, height: 900 },
    ...opts.contextOptions,
  });
  const page = await context.newPage();
  const s = new Session(target, context, page);
  let failure: string | undefined;
  try {
    await scenario(s);
  } catch (e) {
    failure = (e as Error).message.split('\n')[0];
    if (target.name === 'ref') {
      await context.close();
      const recent = JSON.stringify(s.obs.slice(-6), null, 1);
      throw new Error(`scenario failed on the reference Redmine (spec bug?): ${(e as Error).message}\nlast observations: ${recent}`);
    }
  }
  const out = [...s.obs];
  if (failure) out.push(['scenario error', failure]);
  out.push(['dialogs', s.dialogs]);
  out.push(['javascript errors', s.jsErrors]);
  out.push(['http errors', s.httpErrors]);
  await context.close();
  // Absolute URLs (back_url etc.) contain the target's own origin.
  const origin = new URL(target.baseURL).origin;
  const host = new URL(target.baseURL).host;
  // Asset digests are computed independently by Rails and buropher (same content, different hash).
  return JSON.parse(JSON.stringify(out).replace(/(\/assets\/[\w./-]+?)-[0-9a-f]{8}(\.\w+)/g, '$1$2').split(origin).join('{origin}').split(encodeURIComponent(origin)).join('{origin}').split(host).join('{host}'));
}

export function dualTest(title: string, scenario: Scenario, opts: DualOptions = {}) {
  test(title, async ({ browser }, testInfo) => {
    const results: Record<string, Obs[]> = {};
    // E2E_ONLY=ref|cand runs a single side (for writing or debugging a scenario).
    const only = process.env.E2E_ONLY;
    for (const t of targets) if (!only || only === t.name) results[t.name] = await runOn(browser, t, scenario, opts);
    const dir = path.join(__dirname, '..', 'test-results', 'observations');
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, title.replace(/[^a-z0-9]+/gi, '_') + '.json'), JSON.stringify(results, null, 2));
    if (only) return;
    await testInfo.attach('observations.json', {
      body: JSON.stringify(results, null, 2),
      contentType: 'application/json',
    });
    expect(results.cand).toEqual(results.ref);
  });
}
