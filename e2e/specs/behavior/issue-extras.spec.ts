import { dualTest, collapse } from '../../lib/dual';

dualTest('issue: history tabs, reactions, quote reply, inline journal edit', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/2');
  s.takeRequests();

  // History / Notes / Property changes tabs are switched client side.
  for (const tab of ['notes', 'properties', 'history']) {
    await page.click(`#tab-${tab}`);
    s.note(`visible journals on ${tab}`, await page.locator('#history .journal:visible').evaluateAll((j) => j.map((e) => e.id)));
    s.noteURL(`url after ${tab} tab`);
  }
  s.noteRequests('tabs');

  // Reaction toggle on the issue (data-remote post, response replaces the button).
  await page.click('.issue .reaction-button-wrapper a.reaction-button');
  await s.ajaxIdle();
  s.noteRequests('reaction');
  s.note('reaction after click', collapse(await page.locator('.issue .reaction-button-wrapper').first().innerText()));
  s.note('reaction classes', await page.locator('.issue .reaction-button-wrapper a').first().getAttribute('class'));

  // Quote the description into the notes (Stimulus quote-reply controller fetches /issues/2/quoted).
  await page.click('.description a.icon-quote');
  await s.ajaxIdle();
  s.noteRequests('quote description');
  s.note('notes after quote', await page.locator('#issue_notes').inputValue());
  s.note('update form shown', await s.visible('#update'));

  // Inline edit of a journal note (data-remote get → form replaces the note, then PATCH .js).
  await page.click('#change-3 .journal-actions .drdn-trigger');
  await page.click('#change-3 .journal-actions a.icon-edit');
  await s.ajaxIdle();
  s.noteRequests('journal edit form');
  s.note('journal edit form visible', await s.visible('#journal-3-form'));
  await page.fill('#journal-3-form textarea', 'Edited *note* from e2e');
  await page.click('#journal-3-form input[type=submit]');
  await s.ajaxIdle();
  s.noteRequests('journal update');
  s.note('journal 3 notes', collapse(await page.locator('#journal-3-notes').innerHTML()));
  s.note('journal 3 header', collapse(await page.locator('#change-3 h4').innerText()));
});

dualTest('issue notes: mention, issue and wiki page autocomplete (tribute)', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/issues/2');
  await page.click('#content > .contextual a.icon-edit');
  s.takeRequests();
  const notes = page.locator('#issue_notes');
  await notes.click();
  await notes.pressSequentially('Hello @jsm', { delay: 60 });
  await page.waitForSelector('.tribute-container li', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('mention lookup');
  s.note('mention suggestions', await page.locator('.tribute-container li').allInnerTexts());
  await page.keyboard.press('Enter');
  s.note('after mention', await notes.inputValue());

  await notes.pressSequentially(' see #rec', { delay: 60 });
  await page.waitForSelector('.tribute-container li', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('issue lookup');
  s.note('issue suggestions', await page.locator('.tribute-container li').allInnerTexts());
  await page.keyboard.press('Enter');
  s.note('after issue', await notes.inputValue());

  await notes.pressSequentially(' and [[Ano', { delay: 60 });
  await page.waitForSelector('.tribute-container li', { state: 'visible' });
  await s.ajaxIdle();
  s.noteRequests('wiki lookup');
  s.note('wiki suggestions', await page.locator('.tribute-container li').allInnerTexts());
  await page.keyboard.press('Enter');
  s.note('after wiki', await notes.inputValue());
}, { readOnly: true });

dualTest('bulk edit from the context menu', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/projects/ecookbook/issues?set_filter=1&f[]=status_id&op[status_id]=o&sort=id');
  await page.click('#issue-1 td.id', { modifiers: ['Control'] });
  await page.click('#issue-3 td.id', { modifiers: ['Control'] });
  await page.click('#issue-3 td.id', { button: 'right' });
  await page.waitForSelector('#context-menu ul', { state: 'visible' });
  await s.ajaxIdle();
  s.takeRequests();
  await s.nav(() => page.click('#context-menu a.icon-edit'));
  s.noteURL('bulk edit url');
  await s.noteTexts('bulk edit issues', '#bulk-selection li, #content > ul li');
  await page.selectOption('#issue_priority_id', { label: 'High' });
  // Changing the tracker re-renders the form by XHR (bulk_edit with the form data).
  await page.selectOption('#issue_tracker_id', '2');
  await s.ajaxIdle();
  s.noteRequests('tracker change');
  s.note('priority kept', await page.locator('#issue_priority_id').inputValue());
  await page.fill('#notes', 'bulk note');
  await s.nav(() => page.click('#bulk_edit_form input[type=submit][name=commit], #bulk_edit_form input[type=submit]'));
  s.noteURL('after submit');
  s.noteRequests('submit');
  await s.noteText('flash', '#flash_notice, #flash_error');
  s.note('rows', await page.locator('#issue-1 td.tracker, #issue-1 td.priority, #issue-3 td.tracker, #issue-3 td.priority').allInnerTexts());
});

dualTest('my account: show API key, preferences, and password change validation', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/my/account');
  s.takeRequests();
  const show = page.locator('#sidebar a:has-text("Show")');
  if ((await show.count()) > 0) {
    await show.first().click();
    await s.ajaxIdle();
    s.noteRequests('show api key');
    s.note('api key shown', await s.visible('#api-access-key'));
    s.note('api key length', (await page.locator('#api-access-key').innerText()).trim().length);
  }
  await page.selectOption('#pref_comments_sorting', 'desc');
  await page.check('#pref_warn_on_leaving_unsaved');
  await s.nav(() => page.click('#my_account_form input[type=submit]'));
  s.noteURL('after save');
  await s.noteText('flash', '#flash_notice');
  await page.goto('/my/password');
  await page.fill('#password', 'wrong');
  await page.fill('#new_password', 'newpassword1');
  await page.fill('#new_password_confirmation', 'newpassword1');
  await s.nav(() => page.click('#content input[type=submit]'));
  s.noteURL('after wrong password');
  await s.noteText('error', '#flash_error, #errorExplanation');
  s.noteRequests('forms');
});

dualTest('news: add news toggle form with preview, then comment', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/projects/ecookbook/news');
  s.note('form hidden', !(await s.visible('#add-news')));
  await page.click('#content .contextual a.icon-add');
  s.note('form shown', await s.visible('#add-news'));
  await page.fill('#news_title', 'E2E news');
  await page.fill('#news_description', 'Body with **bold**');
  await page.click('#add-news .jstTabs a.tab-preview');
  await s.ajaxIdle();
  s.note('preview', collapse(await page.locator('#add-news .wiki-preview').innerHTML()));
  s.takeRequests();
  await s.nav(() => page.click('#add-news input[name=commit]'));
  s.noteURL('after create');
  s.noteRequests('create');
  await s.noteTexts('news titles', '#content article.news-article h3');
  await page.click('#content article.news-article h3 a:has-text("E2E news")');
  await page.waitForLoadState('load');
  await page.click('a[onclick*="add_comment_form"] >> nth=-1');
  s.note('comment form visible', await s.visible('#add_comment_form'));
  await page.fill('#comment_comments', 'e2e comment');
  await s.nav(() => page.click('#add_comment_form input[type=submit]'));
  s.noteURL('after comment');
  await s.noteTexts('comments', '#comments .journal .wiki');
  s.noteRequests('comment');
});

dualTest('forum: reply with quote', async (s) => {
  const { page } = s;
  await s.login('jsmith', 'jsmith');
  await page.goto('/boards/1/topics/1');
  s.takeRequests();
  await page.click('#content .message.reply a.icon-quote, #content div.contextual a.icon-quote');
  await s.ajaxIdle();
  s.noteRequests('quote');
  s.note('reply visible', await s.visible('#reply'));
  s.note('reply subject', await page.locator('#message_subject').inputValue());
  s.note('reply text', await page.locator('#message_content').inputValue());
  await s.nav(() => page.click('#reply input[type=submit][name=commit]'));
  s.noteURL('after reply');
  s.noteRequests('reply');
  await s.noteTexts('replies', '#replies .message .contextual + h4, #replies h4');
});

dualTest('admin: custom field form reacts to format change; user form generate password', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/custom_fields/new?type=IssueCustomField');
  s.takeRequests();
  s.note('initial fields', await s.texts('#custom_field_form label'));
  await page.selectOption('#custom_field_field_format', 'list');
  await s.ajaxIdle();
  s.noteRequests('format change');
  s.note('list fields', await s.texts('#custom_field_form label'));
  await page.fill('#custom_field_name', 'E2E list');
  await page.fill('#custom_field_possible_values', 'a\nb\nc');
  await page.check('#custom_field_tracker_ids_1');
  await s.nav(() => page.click('#custom_field_form input[name=commit]'));
  s.noteURL('after create');
  await s.noteText('flash', '#flash_notice');

  await page.goto('/users/new');
  s.note('password fields enabled', await page.locator('#user_password').isEnabled());
  await page.check('#user_generate_password');
  s.note('password fields after generate', await page.locator('#user_password').isEnabled());
  s.noteRequests('misc');
});
