import { dualTest } from '../../lib/dual';

dualTest('issues list context menu: select rows, bulk change status via submenu', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/issues?set_filter=1&f[]=status_id&op[status_id]=*&sort=id');
  s.takeRequests();

  // Click selects a row; ctrl+click adds another; right click opens the menu for the selection.
  await page.click('#issue-1 td.subject', { modifiers: [] , position: { x: 2, y: 2 } }).catch(() => page.click('#issue-1 td.id'));
  await page.click('#issue-2 td.id', { modifiers: ['Control'] });
  s.note('selected rows', await page.locator('tr.issue.context-menu-selection').evaluateAll((t) => t.map((e) => e.id)));
  s.note('checked boxes', await page.locator('tr.issue input[type=checkbox]:checked').count());

  await page.click('#issue-2 td.id', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('open context menu');
  s.note('menu items', await page.locator('#context-menu > ul > li > a').allInnerTexts());

  await page.hover('#context-menu li.folder:has(> a:text-is("Status"))');
  s.note('status submenu', await page.locator('#context-menu li.folder:has(> a:text-is("Status")) > ul a').allInnerTexts());
  await page.click('#context-menu li.folder:has(> a:text-is("Status")) > ul a:text-is("Feedback")');
  await page.waitForLoadState('load');
  await page.waitForURL(/\/issues/);
  s.noteRequests('bulk update');
  s.noteURL('after bulk update');
  await s.noteText('flash', '#flash_notice');
  s.note('status column', await page.locator('#issue-1 td.status, #issue-2 td.status').allInnerTexts());

  // Escape / outside click hides the menu.
  await page.click('#issue-3 td.id', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  await page.click('#content h2');
  s.note('menu hidden after outside click', !(await s.visible('#context-menu')));

  // "Select all" toggle checkbox in the header.
  await page.click('table.issues thead input.toggle-selection');
  s.note('all selected', await page.locator('tr.issue.context-menu-selection').count());
  s.noteRequests('misc');
});

dualTest('issues list context menu: delete with confirm (dismissed and accepted)', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/issues?set_filter=1&f[]=status_id&op[status_id]=*&sort=id');
  await page.click('#issue-5 td.id', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.takeRequests();
  s.dialogResponse = 'dismiss';
  await page.click('#context-menu a.icon-del');
  s.noteURL('after dismissed confirm');
  s.noteRequests('dismissed');
  await page.click('#issue-5 td.id', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.takeRequests();
  s.dialogResponse = 'accept';
  await page.click('#context-menu a.icon-del');
  await page.waitForLoadState('load');
  await page.waitForTimeout(300);
  await page.waitForLoadState('load');
  s.noteRequests('accepted');
  s.noteURL('after delete');
  s.note('issue 5 gone', (await page.locator('#issue-5').count()) === 0);
});
