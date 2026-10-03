import { dualTest } from '../../lib/dual';

dualTest('watchers: watch/unwatch toggle, add via modal autocomplete, remove', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/2');
  s.takeRequests();
  await s.noteText('watchers initially', '#watchers');

  // Watch / unwatch link (rails-ujs data-remote + data-method=post/delete).
  await page.click('#content > .contextual a.issue-2-watcher');
  await s.ajaxIdle();
  s.note('watch link after watch', await page.locator('#content > .contextual a.issue-2-watcher').first().getAttribute('class'));
  await s.noteText('watchers after watch', '#watchers');
  await page.click('#content > .contextual a.issue-2-watcher');
  await s.ajaxIdle();
  await s.noteText('watchers after unwatch', '#watchers');
  s.noteRequests('watch toggle');

  // Add watchers via the modal.
  await page.click('#watchers .contextual a');
  await page.waitForSelector('#ajax-modal #new-watcher-form', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('open modal');
  await s.noteText('modal title', '#ajax-modal h3.title');
  s.note('candidates', await s.texts('#users_for_watcher label'));
  await page.locator('#user_search').pressSequentially('smi', { delay: 50 });
  await page.waitForTimeout(800);
  await s.ajaxIdle();
  s.noteRequests('autocomplete');
  s.note('candidates after search', await s.texts('#users_for_watcher label'));
  await page.check('#users_for_watcher input[type=checkbox] >> nth=0');
  await page.click('#new-watcher-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('add');
  s.note('modal closed', !(await s.visible('#ajax-modal')));
  await s.noteText('watchers after add', '#watchers');

  // Remove (data-remote delete with confirm? no confirm for watchers).
  await page.click('#watchers ul.watchers a.delete >> nth=0');
  await s.ajaxIdle();
  s.noteRequests('remove');
  await s.noteText('watchers after remove', '#watchers');
});

dualTest('relations: add via XHR form (valid, invalid, precedes with delay), delete', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/7');
  s.takeRequests();
  await s.noteText('relations initially', '#relations');
  await page.click('#relations a:text-is("Add")');
  s.note('form visible', await s.visible('#new-relation-form'));
  s.note('focus', await page.evaluate(() => document.activeElement?.id));

  await page.fill('#relation_issue_to_id', '9999');
  await page.click('#new-relation-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('invalid');
  await s.noteText('error', '#relations #errorExplanation');

  await page.selectOption('#relation_relation_type', 'precedes');
  s.note('delay visible for precedes', await s.visible('#predecessor_fields'));
  await page.fill('#relation_issue_to_id', '8');
  await page.fill('#relation_delay', '2');
  await page.click('#new-relation-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('precedes');
  await s.noteTexts('relations rows', '#relations table.list tr[id^=relation-]');
  s.note('issue field cleared', await page.locator('#relation_issue_to_id').inputValue());

  // Multiple ids in one go.
  await page.selectOption('#relation_relation_type', 'relates');
  await page.fill('#relation_issue_to_id', '3, 6');
  await page.click('#new-relation-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('relates multiple');
  await s.noteTexts('relations rows after multiple', '#relations table.list tr[id^=relation-]');

  // Delete with confirm.
  await page.click('#relations table.list tr[id^=relation-] >> nth=0 >> a.icon-link-break');
  await s.ajaxIdle();
  s.noteRequests('delete');
  await s.noteTexts('relations rows after delete', '#relations table.list tr[id^=relation-]');
});
