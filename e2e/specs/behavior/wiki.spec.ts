import { Page } from '@playwright/test';
import { dualTest, collapse } from '../../lib/dual';

async function selectAll(page: Page, from: number, to: number) {
  await page.locator('#content_text').evaluate(
    (t: HTMLTextAreaElement, [a, b]) => {
      t.focus();
      t.setSelectionRange(a, b);
    },
    [from, to],
  );
}

async function appendLine(page: Page, text: string) {
  await page.locator('#content_text').evaluate((t: HTMLTextAreaElement, line) => {
    t.value += line;
    t.setSelectionRange(t.value.length, t.value.length);
  }, text);
}

dualTest('wiki: create page with toolbar buttons and preview, then edit a section', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/wiki/E2E_page');
  s.noteURL('new page url');
  s.takeRequests();
  s.note('toolbar buttons', await page.locator('#content_text').locator('xpath=ancestor::div[contains(@class,"jstBlock")]').locator('.jstElements button').evaluateAll((b) => b.map((e) => e.className)));

  // h1 button on an empty line, then text.
  await page.fill('#content_text', 'Title');
  await selectAll(page, 0, 5);
  await page.click('.jstElements button.jstb_h1');
  s.note('after h1', await page.locator('#content_text').inputValue());

  await appendLine(page, '\n\nSection one');
  const v1 = await page.locator('#content_text').inputValue();
  await selectAll(page, v1.length - 'Section one'.length, v1.length);
  await page.click('.jstElements button.jstb_h2');
  await appendLine(page, '\n\nfirst item\nsecond item');
  const v2 = await page.locator('#content_text').inputValue();
  await selectAll(page, v2.length - 'first item\nsecond item'.length, v2.length);
  await page.click('.jstElements button.jstb_ul');
  await appendLine(page, '\n\nSection two');
  const v3 = await page.locator('#content_text').inputValue();
  await selectAll(page, v3.length - 'Section two'.length, v3.length);
  await page.click('.jstElements button.jstb_h2');
  await appendLine(page, '\n\nemphasis here');
  const v4 = await page.locator('#content_text').inputValue();
  await selectAll(page, v4.length - 'here'.length, v4.length);
  await page.click('.jstElements button.jstb_em');
  await appendLine(page, '\n\nCookBook documentation');
  const v5 = await page.locator('#content_text').inputValue();
  await selectAll(page, v5.length - 'CookBook documentation'.length, v5.length);
  await page.click('.jstElements button.jstb_link');
  s.note('source after toolbar', await page.locator('#content_text').inputValue());

  // Preview tab.
  await page.click('.jstTabs a.tab-preview');
  await s.ajaxIdle();
  await page.waitForSelector('.wiki-preview', { state: 'visible' });
  s.noteRequests('preview');
  s.note('preview', collapse(await page.locator('.wiki-preview').innerHTML()));
  await page.click('.jstTabs a.tab-edit');

  await page.fill('#content_comments', 'created by e2e');
  await page.click('#wiki_form input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after save');
  s.noteRequests('save');
  s.note('rendered', collapse(await page.locator('#content .wiki.wiki-page').innerHTML()));

  // Section edit links appear next to the headings.
  s.note('section edit links', await page.locator('#content .wiki.wiki-page .contextual a').evaluateAll((as) => as.map((a) => a.getAttribute('href'))));
  await page.click('#content .wiki.wiki-page .contextual a[href*="section=3"]');
  await page.waitForLoadState('load');
  s.noteURL('section edit');
  s.note('section text', await page.locator('#content_text').inputValue());
  s.note('section hidden fields', await page.locator('#wiki_form input[type=hidden]').evaluateAll((els) =>
    els.map((e) => (e as HTMLInputElement).name).filter((n) => n !== 'authenticity_token')));
  await page.fill('#content_text', '## Section two\n\nreplaced by section edit');
  await page.click('#wiki_form input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after section save');
  s.noteRequests('section edit');
  s.note('rendered after section edit', collapse(await page.locator('#content .wiki.wiki-page').innerText()));

  // History and diff.
  await page.goto('/projects/ecookbook/wiki/E2E_page/history');
  await s.noteTexts('history rows', 'table.wiki-page-versions tbody tr');
  await page.click('form input[type=submit][value="View differences"]');
  await page.waitForLoadState('load');
  s.noteURL('diff');
  await s.noteText('diff', '#content .text-diff');
  s.noteRequests('history');
});

dualTest('wiki: precode language menu and table generator', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/wiki/E2E_code/edit');
  await page.fill('#content_text', 'puts 1');
  await selectAll(page, 0, 6);
  await page.click('.jstElements button.jstb_precode');
  // The language menu is built from window.userHlLanguages, which the server renders.
  s.note('language menu', (await page.locator('ul.ui-menu li').allInnerTexts()).slice(0, 12));
  await page.locator('ul.ui-menu li').filter({ hasText: /^ruby$/ }).dispatchEvent('mousedown');
  s.note('after precode', await page.locator('#content_text').inputValue());

  // Table generator: 2 columns x 2 rows.
  await page.locator('#content_text').evaluate((t: HTMLTextAreaElement) => {
    t.value += '\n\n';
    t.setSelectionRange(t.value.length, t.value.length);
  });
  await page.click('.jstElements button.jstb_table');
  await page.locator('table.table-generator td[data-row="2"][data-col="2"]').dispatchEvent('mousedown');
  s.note('after table', await page.locator('#content_text').inputValue());
});
