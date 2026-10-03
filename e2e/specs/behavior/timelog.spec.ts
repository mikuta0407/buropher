import { dualTest } from '../../lib/dual';

dualTest('time entry: issue autocomplete, project/activity refresh, create', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/time_entries/new');
  s.takeRequests();

  // jQuery UI autocomplete on the issue field.
  await page.locator('#time_entry_issue_id').pressSequentially('rec', { delay: 60 });
  await page.waitForSelector('ul.ui-autocomplete li', { state: 'visible' });
  await s.ajaxIdle();
  s.noteLastRequest('autocomplete');
  s.note('suggestions', await page.locator('ul.ui-autocomplete li').allInnerTexts());
  await page.locator('ul.ui-autocomplete li').first().click();
  await s.ajaxIdle();
  s.note('issue id', await page.locator('#time_entry_issue_id').inputValue());
  await s.noteText('issue label', '#time_entry_issue');
  s.noteRequests('select suggestion');

  await page.fill('#time_entry_hours', '1:30');
  await page.fill('#time_entry_comments', 'e2e work');
  s.note('activities', await page.locator('#time_entry_activity_id option').allTextContents());
  await page.click('#new_time_entry input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after create');
  s.noteRequests('create');
  await s.noteText('flash', '#flash_notice');
  await s.noteTexts('first row', 'table.time-entries tbody tr:first-child td');
});

dualTest('time entries list: context menu bulk edit link and filters', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/time_entries');
  s.takeRequests();
  s.note('rows', await page.locator('table.time-entries tbody tr').evaluateAll((t) => t.map((e) => e.id)));
  await page.locator('table.time-entries tbody tr.hascontextmenu td.activity').first().click({ button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('context menu');
  s.note('menu', await page.locator('#context-menu > ul > li > a').allInnerTexts());
  await page.click('#content h2');
  await page.selectOption('#add_filter_select', 'activity_id');
  await page.selectOption('#values_activity_id_1', { index: 0 });
  await page.click('#query_form p.buttons a.icon-checked');
  await page.waitForLoadState('load');
  s.noteURL('filtered');
  await s.noteText('total', '.query-totals');
  // Details / Report tabs.
  await page.click('#content .tabs a:has-text("Report")');
  await page.waitForLoadState('load');
  s.noteURL('report');
  await s.nav(() => page.selectOption('#criterias', 'user'));
  s.noteURL('report with criteria');
  await s.noteTexts('report rows', '#time-report tbody tr');
  s.noteRequests('filters and report');
});
