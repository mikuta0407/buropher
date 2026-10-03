import * as path from 'node:path';
import { dualTest, collapse } from '../../lib/dual';

const uploadFile = path.join(__dirname, '..', '..', 'fixtures', 'e2e-upload.txt');

dualTest('issue edit: status change refreshes form, note preview tab, upload attachment, submit', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/1');
  s.takeRequests();

  // "Edit" shows the hidden update form and focuses notes.
  s.note('update form initially visible', await s.visible('#update'));
  await page.click('#content > .contextual a.icon-edit');
  s.note('update form visible after Edit', await s.visible('#update'));
  s.note('notes focused', await page.evaluate(() => document.activeElement?.id));

  // Changing the status posts the form to /issues/1/edit.js and replaces the attributes.
  await page.selectOption('#issue_status_id', '2');
  await s.ajaxIdle();
  s.noteRequests('status change');
  s.note('status after refresh', await page.locator('#issue_status_id').inputValue());
  s.note('status options', await page.locator('#issue_status_id option').allTextContents());
  s.note('assignee options', await page.locator('#issue_assigned_to_id option').allTextContents());

  // Note with the preview tab.
  await page.fill('#issue_notes', 'E2E *note* with a link to #2 and `code`');
  await page.click('#add_notes .jstTabs a.tab-preview');
  await s.ajaxIdle();
  await page.waitForSelector('#add_notes .wiki-preview', { state: 'visible' });
  s.noteRequests('preview');
  s.note('preview html', collapse(await page.locator('#add_notes .wiki-preview').innerHTML()));
  s.note('textarea hidden in preview', !(await page.locator('#issue_notes').isVisible()));
  await page.click('#add_notes .jstTabs a.tab-edit');
  s.note('textarea back', await page.locator('#issue_notes').isVisible());

  // Toolbar button wraps the selection.
  await page.fill('#issue_notes', 'bold me');
  await page.locator('#issue_notes').evaluate((t: HTMLTextAreaElement) => t.setSelectionRange(0, 4));
  await page.click('#add_notes .jstElements button.jstb_strong');
  s.note('after bold button', await page.locator('#issue_notes').inputValue());

  // Upload through the attachments widget (ajax upload to /uploads.js, then the token is kept in a hidden field).
  await page.setInputFiles('#add_attachments input.file_selector', uploadFile);
  await page.waitForSelector('#add_attachments .attachments_fields input.token', { state: 'attached' });
  await s.ajaxIdle();
  await page.waitForFunction(() => {
    const t = document.querySelector<HTMLInputElement>('#add_attachments input.token');
    return !!t && t.value !== '';
  });
  s.noteRequests('upload');
  s.note('upload fields', await page.locator('#add_attachments .attachments_fields input').evaluateAll((els) =>
    els.map((e) => `${(e as HTMLInputElement).name}=${(e as HTMLInputElement).type === 'hidden' && (e as HTMLInputElement).name.endsWith('[token]') ? '<token>' : (e as HTMLInputElement).value}`)));
  await page.fill('#add_attachments .attachments_fields input.description', 'uploaded by e2e');

  await page.click('#issue-form input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after submit');
  s.noteRequests('submit');
  await s.noteText('flash', '#flash_notice');
  await s.noteText('status', '.issue .attributes .status .value');
  await s.noteTexts('last journal details', '#history div.journal >> nth=-1 >> ul.details li');
  await s.noteText('last journal notes', '#history div.journal >> nth=-1 >> div.wiki');
  await s.noteTexts('attachments', '.attachments table td:first-child');
});

dualTest('issue edit: unsaved changes warning on leave (dismiss keeps page, accept leaves)', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/2/edit');
  await page.fill('#issue_notes', 'something not saved');
  s.dialogResponse = 'dismiss';
  await page.click('#top-menu a.home');
  await page.waitForTimeout(500);
  s.noteURL('after dismissing');
  s.dialogResponse = 'accept';
  await page.click('#top-menu a.home');
  await page.waitForLoadState('load');
  s.noteURL('after accepting');

  // Submitting the form itself must not warn.
  await page.goto('/issues/2/edit');
  await page.fill('#issue_notes', 'saved note');
  await page.click('#issue-form input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after submit');
  s.noteRequests('all');
});

dualTest('new issue: tracker change refresh, dates, category, create and continue', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/issues/new');
  s.takeRequests();
  s.note('tracker options', await page.locator('#issue_tracker_id option').allTextContents());
  s.note('custom fields', await s.texts('#all_attributes label'));
  await page.selectOption('#issue_tracker_id', '2');
  await s.ajaxIdle();
  s.noteRequests('tracker change');
  s.note('custom fields after tracker change', await s.texts('#all_attributes label'));
  await page.fill('#issue_subject', 'E2E new issue');
  await page.fill('#issue_description', 'Description');
  await page.fill('#issue_start_date', '2026-02-01');
  await page.fill('#issue_due_date', '2026-02-10');
  s.note('start date type', await page.locator('#issue_start_date').getAttribute('type'));
  await page.selectOption('#issue_priority_id', { label: 'High' });
  await page.click('#issue-form input[name=continue]');
  await page.waitForLoadState('load');
  s.noteURL('after create and continue');
  await s.noteText('flash', '#flash_notice');
  s.note('tracker kept', await page.locator('#issue_tracker_id').inputValue());
  s.noteRequests('create');
});

dualTest('new issue: validation errors keep the input', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/issues/new');
  await page.fill('#issue_start_date', '2026-02-10');
  await page.fill('#issue_due_date', '2026-02-01');
  await page.click('#issue-form input[name=commit]');
  await page.waitForLoadState('load');
  s.noteURL('after invalid submit');
  await s.noteTexts('errors', '#errorExplanation li');
  s.note('start date kept', await page.locator('#issue_start_date').inputValue());
  s.noteRequests('all');
});

