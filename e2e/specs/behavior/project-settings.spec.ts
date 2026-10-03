import { dualTest } from '../../lib/dual';

dualTest('project settings: members modal (search, add), edit roles inline, remove', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/settings/members');
  s.takeRequests();
  await s.noteTexts('members', 'table.members tbody tr td.name');

  await page.click('#tab-content-members a.icon-add');
  await page.waitForSelector('#ajax-modal #principal_search', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('open modal');
  await s.noteText('modal title', '#ajax-modal h3.title');
  s.note('principals', await s.texts('#principals_for_new_member label'));
  await page.locator('#principal_search').pressSequentially('mis', { delay: 60 });
  await page.waitForTimeout(800);
  await s.ajaxIdle();
  s.noteLastRequest('search');
  s.note('principals after search', await s.texts('#principals_for_new_member label'));
  await page.check('#principals_for_new_member input[type=checkbox] >> nth=0');
  await page.check('#ajax-modal .roles-selection input >> nth=0');
  await page.click('#ajax-modal input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('add');
  s.note('modal closed', !(await s.visible('#ajax-modal')));
  await s.noteTexts('members after add', 'table.members tbody tr td.name');

  // Inline role edit.
  const row = page.locator('table.members tbody tr').last();
  await row.locator('a.icon-edit').click();
  await s.ajaxIdle();
  s.note('edit form visible', await row.locator('form').isVisible());
  await row.locator('form input[type=checkbox][name="membership[role_ids][]"] >> nth=1').check();
  await row.locator('form input[type=submit]').click();
  await s.ajaxIdle();
  s.noteRequests('edit roles');
  await s.noteTexts('roles column', 'table.members tbody tr td.roles span');

  // Remove with confirm (data-remote delete).
  await page.locator('table.members tbody tr').last().locator('a.icon-del').click();
  await s.ajaxIdle();
  s.noteRequests('remove');
  await s.noteTexts('members after remove', 'table.members tbody tr td.name');
});

dualTest('project settings: tabs switch client side and modules save', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/settings');
  s.takeRequests();
  s.note('tabs', await s.texts('#content .tabs ul li a'));
  await page.click('#tab-issues');
  s.noteURL('url after tab click (history.replaceState)');
  s.note('issues tab visible', await s.visible('#tab-content-issues'));
  s.note('info tab hidden', !(await s.visible('#tab-content-info')));
  s.noteRequests('tab click');
  await page.click('#tab-info');
  await page.uncheck('#tab-content-info input[name="project[enabled_module_names][]"][value="news"]');
  await page.click('#tab-content-info input[type=submit][name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after save');
  await s.noteText('flash', '#flash_notice');
  s.note('menu', await s.texts('#main-menu li a'));
  s.noteRequests('save modules');

  // Versions tab: "close completed" with confirm, and the categories tab new link.
  await page.click('#tab-versions');
  s.note('versions rows', await page.locator('#tab-content-versions table.versions tbody tr').count());
});
