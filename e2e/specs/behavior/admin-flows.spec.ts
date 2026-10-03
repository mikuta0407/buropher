import * as path from 'node:path';
import { dualTest } from '../../lib/dual';
import { REFERENCE_DIR } from '../../lib/targets';

const importCSV = path.join(REFERENCE_DIR, 'redmine', 'test', 'fixtures', 'files', 'import_issues.csv');

dualTest('issue import: upload CSV, settings, mapping XHR refresh, run with progress polling', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/imports/new?project_id=ecookbook');
  s.noteURL('new import');
  await page.setInputFiles('input#file', importCSV);
  s.takeRequests();
  await s.nav(() => page.click('#content input[type=submit]'));
  s.note('settings url', s.path().replace(/\/imports\/[0-9a-f]+\//, '/imports/{id}/'));
  await page.selectOption('#import_settings_separator', ';');
  await page.selectOption('#import_settings_date_format', '%Y-%m-%d');
  await s.nav(() => page.click('#import-form input[type=submit]'));
  s.note('mapping url', s.path().replace(/\/imports\/[0-9a-f]+\//, '/imports/{id}/'));
  s.note('auto mapped', await page.locator('#import-form select').evaluateAll((sels) =>
    sels.map((e) => `${(e as HTMLSelectElement).name}=${(e as HTMLSelectElement).value}`)));
  // Changing the project re-renders the mapping fields by XHR.
  await page.selectOption('#import_mapping_project_id', { label: 'OnlineStore' });
  await s.ajaxIdle();
  s.note('tracker options after project change', await page.locator('#import_mapping_tracker option').allTextContents());
  await page.selectOption('#import_mapping_project_id', { label: 'eCookbook' });
  await s.ajaxIdle();
  await page.selectOption('#import_mapping_tracker', { label: 'Bug' }).catch(() => undefined);
  await page.selectOption('#import_mapping_subject', { label: 'subject' });
  await page.selectOption('#import_mapping_priority', { label: 'priority' }).catch(() => undefined);
  const reqs = s.takeRequests().map((r) => r.replace(/\/imports\/[0-9a-f]+/g, '/imports/{id}'));
  s.note('mapping requests', reqs.map((r) => r.replace(/authenticity_token=[^"&]*/g, '')));
  await page.click('#import-form input[type=submit][name=commit]');
  await page.waitForURL(/\/imports\/[0-9a-f]+(\?|$)/, { timeout: 30_000 });
  await page.waitForLoadState('load');
  s.note('result url', s.path().replace(/\/imports\/[0-9a-f]+/, '/imports/{id}'));
  await s.noteText('result', '#content p');
  s.note('saved items', (await s.texts('#saved-items li, table#saved-items td.subject, #saved-items a')).length);
  const runReqs = s.takeRequests().map((r) => r.replace(/\/imports\/[0-9a-f]+/g, '/imports/{id}'));
  s.note('run requests (deduplicated)', [...new Set(runReqs.map((r) => r.replace(/\s.*$/, '')))]);
});

dualTest('workflow edit: select role/tracker, check-all toggles, save', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/workflows/edit');
  s.takeRequests();
  await page.selectOption('#role_id', '2');
  await page.selectOption('#tracker_id', '1');
  await s.nav(() => page.click('#workflow_form input[type=submit], form input[type=submit][value="Edit"]'));
  s.noteURL('after select');
  const before = await page.locator('table.workflows input[type=checkbox]:checked').count();
  s.note('checked before', before);
  // The ✓ header icons toggle a whole row/column (toggleCheckboxesBySelector).
  await page.locator('table.workflows thead a.icon-checked, table.workflows a.icon-checked').first().click();
  s.note('checked after first toggle', await page.locator('table.workflows input[type=checkbox]:checked').count());
  await s.nav(() => page.click('#workflow_form input[type=submit][name=commit], form#workflow_form input[type=submit]:not([value="Edit"])'));
  s.noteURL('after save');
  await s.noteText('flash', '#flash_notice');
  s.note('checked after save', await page.locator('table.workflows input[type=checkbox]:checked').count());
  s.noteRequests('workflow');
});

dualTest('admin settings: tabs switch client side, save a tab, enumerations new', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/settings');
  s.note('tabs', await s.texts('#content .tabs li a'));
  await page.click('#tab-display');
  s.noteURL('after tab click');
  s.note('display visible', await s.visible('#tab-content-display'));
  await page.selectOption('#settings_ui_theme', { index: 0 }).catch(() => undefined);
  await page.click('#tab-issues');
  await page.check('#settings_display_subprojects_issues').catch(() => undefined);
  await page.fill('#settings_issues_export_limit', '400');
  s.takeRequests();
  await s.nav(() => page.click('#tab-content-issues input[type=submit]'));
  s.noteURL('after save');
  await s.noteText('flash', '#flash_notice');
  s.note('selected tab after save', await page.locator('#content .tabs a.selected').first().getAttribute('id'));
  s.noteRequests('save');
  await page.goto('/enumerations/new?type=IssuePriority');
  await page.fill('#enumeration_name', 'E2E priority');
  await s.nav(() => page.click('#content input[type=submit]'));
  s.noteURL('after enumeration create');
  s.note('priorities', await s.texts('table.list:nth-of-type(2) td.name'));
});

dualTest('wiki: rename page, protect, delete with confirm', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/wiki/Another_page/rename');
  await page.fill('#wiki_page_title', 'Renamed page');
  await s.nav(() => page.click('#content input[type=submit]'));
  s.noteURL('after rename');
  await s.noteText('flash', '#flash_notice');
  s.takeRequests();
  await page.goto('/projects/ecookbook/wiki/Another_page');
  s.noteURL('old title redirects');
  s.dialogResponse = 'accept';
  await page.click('#content .contextual .drdn-trigger').catch(() => undefined);
  await s.nav(() => page.click('#content .contextual a.icon-lock'));
  s.noteURL('after protect');
  await page.click('#content .contextual .drdn-trigger').catch(() => undefined);
  await s.nav(() => page.click('#content .contextual a.icon-del'));
  s.noteURL('after delete');
  s.noteRequests('protect and delete');
  await s.noteTexts('index', '#content ul.pages-hierarchy li > a');
});

dualTest('account: lost password, register with validation errors (anonymous)', async (s) => {
  const { page } = s;
  await page.goto('/login');
  await s.nav(() => page.click('a.lost_password'));
  s.noteURL('lost password');
  await page.fill('#mail', 'jsmith@somenet.foo');
  await s.nav(() => page.click('#content input[type=submit]'));
  s.noteURL('after lost password');
  await s.noteText('flash', '#flash_notice, #flash_error');
  await page.goto('/account/register');
  s.noteURL('register');
  if (await s.visible('#user_login')) {
    await page.fill('#user_login', 'jsmith');
    await page.fill('#user_password', 'short');
    await page.fill('#user_password_confirmation', 'other');
    await s.nav(() => page.click('#content input[type=submit]'));
    await s.noteTexts('errors', '#errorExplanation li');
  }
  s.noteRequests('all');
});

dualTest('issue delete with logged time: reassign form', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/1');
  s.dialogResponse = 'accept';
  await page.click('#content > .contextual .drdn-trigger');
  await s.nav(() => page.click('#content > .contextual a.icon-del'));
  s.noteURL('destroy confirmation');
  await s.noteText('question', '#content form p');
  s.note('options', await page.locator('#content form input[type=radio]').evaluateAll((r) => r.map((e) => (e as HTMLInputElement).value)));
  await page.check('#todo_reassign');
  await page.fill('#reassign_to_id', '2');
  await s.nav(() => page.click('#content form input[type=submit]'));
  s.noteURL('after delete');
  await s.noteText('flash', '#flash_notice, #flash_error');
  s.noteRequests('all');
});
