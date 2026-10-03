import * as path from 'node:path';
import { dualTest } from '../../lib/dual';

const uploadFile = path.join(__dirname, '..', '..', 'fixtures', 'e2e-upload.txt');

dualTest('new issue: inline new category and version modals, parent autocomplete, watcher search modal', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/issues/new');
  s.takeRequests();

  // New category (modal, XHR create updates the select).
  await page.click('#issue_category_id + a.icon-add, a[title="New category"]');
  await page.waitForSelector('#ajax-modal input#issue_category_name', { state: 'visible' });
  await s.ajaxIdle();
  await s.noteText('category modal title', '#ajax-modal h3.title');
  await page.fill('#issue_category_name', 'E2E category');
  await page.click('#ajax-modal input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('new category');
  s.note('category options', await page.locator('#issue_category_id option').allTextContents());
  s.note('category selected', await page.locator('#issue_category_id option:checked').textContent());

  // New version (modal).
  await page.click('a[title="New version"]');
  await page.waitForSelector('#ajax-modal input#version_name', { state: 'visible' });
  await s.ajaxIdle();
  await page.fill('#version_name', 'E2E version');
  await page.click('#ajax-modal input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('new version');
  s.note('version options', await page.locator('#issue_fixed_version_id option').allTextContents());
  s.note('version selected', await page.locator('#issue_fixed_version_id option:checked').textContent());

  // Parent task autocomplete (jQuery UI autocomplete).
  await page.locator('#issue_parent_issue_id').pressSequentially('cann', { delay: 60 });
  await page.waitForSelector('ul.ui-autocomplete li', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('parent autocomplete');
  s.note('parent suggestions', await page.locator('ul.ui-autocomplete:visible li').allInnerTexts());
  await page.locator('ul.ui-autocomplete:visible li').first().click();
  await s.ajaxIdle();
  s.note('parent id', await page.locator('#issue_parent_issue_id').inputValue());
  s.noteRequests('parent selected (form refresh)');

  // "Search for watchers to add" modal appends checkboxes to the form.
  await page.click('#watchers_form .search_for_watchers a');
  await page.waitForSelector('#ajax-modal #users_for_watcher', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('watcher modal');
  s.note('watcher candidates', await s.texts('#users_for_watcher label'));
  await page.check('#users_for_watcher input[type=checkbox] >> nth=0');
  await page.click('#new-watcher-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('watcher append');
  s.note('watchers in form', await page.locator('#watchers_inputs input[type=checkbox]:checked').evaluateAll((els) =>
    els.map((e) => (e as HTMLInputElement).value)));

  await page.fill('#issue_subject', 'E2E with modals');
  await s.nav(() => page.click('#issue-form input[name=commit]'));
  s.noteURL('after create');
  await s.noteText('attributes', '.issue .attributes');
  await s.noteText('watchers', '#watchers');
  s.noteRequests('create');
});

dualTest('new project: identifier generated from the name, then copy project', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/new');
  await page.locator('#project_name').pressSequentially('My E2E Project 1', { delay: 30 });
  s.note('generated identifier', await page.locator('#project_identifier').inputValue());
  await page.uncheck('#project_enabled_module_names_news');
  s.takeRequests();
  await s.nav(() => page.click('#new_project input[name=commit]'));
  s.noteURL('after create');
  await s.noteText('flash', '#flash_notice');
  s.noteRequests('create');

  await page.goto('/projects/ecookbook/copy');
  s.note('copy name', await page.locator('#project_name').inputValue());
  s.note('copy identifier', await page.locator('#project_identifier').inputValue());
  await page.fill('#project_name', 'Cookbook copy');
  await page.fill('#project_identifier', 'cookbook-copy');
  await s.nav(() => page.click('#content input[type=submit][name=commit]'));
  s.noteURL('after copy');
  await s.noteText('flash after copy', '#flash_notice, #flash_error');
  s.noteRequests('copy');
});

dualTest('project files: upload via the attachment widget, then edit attachment description', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/files/new');
  s.takeRequests();
  await page.setInputFiles('input.file_selector', uploadFile);
  await page.waitForFunction(() => {
    const t = document.querySelector<HTMLInputElement>('.attachments_fields input.token');
    return !!t && t.value !== '';
  });
  await s.ajaxIdle();
  s.noteRequests('upload');
  await page.fill('.attachments_fields input.description', 'e2e file');
  // Remove + re-add is client side; just submit.
  await s.nav(() => page.click('#content input[type=submit][name=commit]'));
  s.noteURL('after submit');
  s.noteRequests('submit');
  await s.noteTexts('files', 'table.list.files td.filename');
  await s.noteTexts('descriptions', 'table.list.files td.description');
});

dualTest('roadmap: sidebar tracker filter and completed versions; version page toggles', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/roadmap');
  s.note('versions', await s.texts('#roadmap h3.version'));
  await page.check('#sidebar input[name=completed]');
  await page.uncheck('#sidebar input[type=checkbox][name="tracker_ids[]"] >> nth=0');
  await s.nav(() => page.click('#sidebar input[type=submit]'));
  s.noteURL('after apply');
  s.note('versions after apply', await s.texts('#roadmap h3.version'));
  await page.goto('/versions/2');
  s.note('version issues', await s.texts('table.related-issues tr td.subject, table.related-issues tr td:nth-child(2)'));
  s.noteRequests('all');
}, { readOnly: true });

dualTest('gantt and calendar: navigation and options', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/issues/gantt?month=1&year=2026');
  s.takeRequests();
  s.note('gantt subjects', (await s.texts('#gantt_area .gantt_subjects .issue-subject, .gantt_subjects div.issue-subject')).slice(0, 10));
  await s.nav(() => page.click('#content a.icon-zoom-in, #content a:has-text("Zoom in")'));
  s.noteURL('after zoom in');
  await page.click('#options legend');
  await page.uncheck('#draw_relations');
  s.note('relations hidden', await page.locator('#gantt_draw_area path').count());
  await page.check('#draw_relations');
  s.note('relations drawn', await page.locator('#gantt_draw_area path, #gantt_draw_area svg *').count() > 0);
  await page.check('#draw_progress_line');
  s.note('progress line drawn', await page.locator('#gantt_draw_area').innerHTML().then((h) => h.length > 0));
  await s.nav(() => page.click('#content p.contextual a:has-text("Next"), a:has-text("Next »")'));
  s.noteURL('gantt next');
  await page.goto('/projects/ecookbook/issues/calendar?month=1&year=2026');
  s.note('calendar issues', (await s.texts('table.cal div.issue a.issue')).slice(0, 10));
  await s.nav(() => page.click('#content a:has-text("Feb")'));
  s.noteURL('calendar next month');
  s.noteRequests('navigation');
}, { readOnly: true });

dualTest('admin: group users modal, role permission toggles, project archive with confirm', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/groups/10/edit?tab=users');
  s.takeRequests();
  s.note('group users', await s.texts('#tab-content-users table.users td.name'));
  await page.click('#tab-content-users a.icon-add');
  await page.waitForSelector('#ajax-modal #user_search', { state: 'visible' });
  await s.ajaxIdle();
  s.note('candidates', await s.texts('#ajax-modal #users label'));
  await page.locator('#user_search').pressSequentially('rob', { delay: 60 });
  await page.waitForTimeout(800);
  await s.ajaxIdle();
  s.note('candidates after search', await s.texts('#ajax-modal #users label'));
  await page.check('#ajax-modal #users input[type=checkbox] >> nth=0');
  await page.click('#ajax-modal input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('add users');
  s.note('group users after add', await s.texts('#tab-content-users table.users td.name'));
  await page.locator('#tab-content-users table.users tr').last().locator('a.icon-del').click();
  await s.ajaxIdle();
  s.noteRequests('remove user');
  s.note('group users after remove', await s.texts('#tab-content-users table.users td.name'));

  await page.goto('/roles/2/edit');
  const before = await page.locator('#permissions input[type=checkbox]:checked').count();
  await page.click('#permissions fieldset legend a, #permissions .toggle-all >> nth=0').catch(() => undefined);
  s.note('checked permissions changed by toggle', (await page.locator('#permissions input[type=checkbox]:checked').count()) !== before);

  await page.goto('/admin/projects');
  s.dialogResponse = 'accept';
  await page.locator('tr.hascontextmenu:has(td.name a:text-is("OnlineStore")) td.identifier').click({ button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.note('admin project menu', await page.locator('#context-menu > ul > li > a').allInnerTexts());
  await s.nav(() => page.click('#context-menu a.icon-lock'));
  s.noteURL('after archive');
  s.note('archived row classes', await page.locator('tr.hascontextmenu:has(td.name a:text-is("OnlineStore"))').first().getAttribute('class').catch(() => 'gone'));
  s.noteRequests('archive');
});

dualTest('time entries: bulk edit via context menu, edit entry', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/time_entries');
  await page.click('table.time-entries tbody tr.hascontextmenu >> nth=0 >> td.activity', { modifiers: ['Control'] });
  await page.click('table.time-entries tbody tr.hascontextmenu >> nth=1 >> td.activity', { modifiers: ['Control'] });
  await page.click('table.time-entries tbody tr.hascontextmenu >> nth=1 >> td.activity', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.takeRequests();
  await s.nav(() => page.click('#context-menu a.icon-edit'));
  s.noteURL('bulk edit');
  await page.selectOption('#time_entry_activity_id', { label: 'QA' });
  await s.nav(() => page.click('#bulk_edit_form input[type=submit]'));
  s.noteURL('after bulk update');
  await s.noteText('flash', '#flash_notice, #flash_error');
  s.note('activities', (await s.texts('table.time-entries td.activity')).slice(0, 4));
  s.noteRequests('bulk');
});

