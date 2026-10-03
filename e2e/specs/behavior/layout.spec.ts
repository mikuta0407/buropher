import { dualTest, Session } from '../../lib/dual';

dualTest(
  'responsive: mobile flyout menu (375px)',
  async (s) => {
    const { page } = s;
    await s.login('jsmith', 'jsmith');
    await page.goto('/projects/ecookbook/issues');
    s.note('toggle visible', await s.visible('.mobile-toggle-button'));
    s.note('flyout visible before', await s.visible('.flyout-menu'));
    await page.click('.mobile-toggle-button');
    await page.waitForTimeout(400);
    s.note('flyout visible after', await s.visible('.flyout-menu'));
    s.note('html classes', await page.evaluate(() => document.documentElement.className));
    s.note('flyout project menu', await s.texts('.flyout-menu .js-project-menu li a'));
    s.note('flyout general menu', await s.texts('.flyout-menu .js-general-menu li a'));
    s.note('flyout profile', await s.texts('.flyout-menu .js-profile-menu li a'));
    s.note('flyout sidebar present', (await page.locator('.flyout-menu .js-sidebar').count()) > 0);
    await page.locator('.flyout-menu .js-project-menu a').filter({ hasText: /^Activity$/ }).first().click();
    await page.waitForLoadState('load');
    s.noteURL('after flyout navigation');
    // Main menu on mobile: tabs collapse into the flyout; the content filter fieldset toggles.
    await page.goto('/projects/ecookbook/issues');
    await page.click('#query_form legend:has-text("Filters")');
    s.note('filters collapsed after legend click', await page.locator('#filters').getAttribute('class'));
    s.noteRequests('all');
  },
  { readOnly: true, viewport: { width: 375, height: 800 }, contextOptions: { isMobile: true, hasTouch: true } },
);

dualTest(
  'sidebar toggle and collapsible fieldsets',
  async (s) => {
    const { page } = s;
    await s.login('jsmith', 'jsmith');
    await page.goto('/projects/ecookbook/issues');
    s.note('sidebar visible', await s.visible('#sidebar'));
    const toggle = page.locator('#sidebar-switch-button, .sidebar-switch');
    if ((await toggle.count()) > 0) {
      await toggle.first().click();
      await page.waitForTimeout(300);
      s.note('main classes after hiding sidebar', await page.locator('#main').getAttribute('class'));
      await page.reload();
      s.note('main classes after reload (persisted in localStorage)', await page.locator('#main').getAttribute('class'));
      await page.locator('#sidebar-switch-button, .sidebar-switch').first().click();
      await page.waitForTimeout(300);
      s.note('main classes after showing sidebar', await page.locator('#main').getAttribute('class'));
    }
    await page.click('#options legend');
    s.note('options fieldset class', await page.locator('#options').getAttribute('class'));
    await page.click('#filters legend');
    s.note('filters fieldset class', await page.locator('#filters').getAttribute('class'));
    s.note('filters content visible', await page.locator('#filters > div').isVisible());

    // Project jump box in the header: open, quick search (XHR), choose.
    await page.click('#project-jump .drdn-trigger');
    s.note('jump open', await page.locator('#project-jump').getAttribute('class'));
    await page.locator('#projects-quick-search').pressSequentially('sub', { delay: 60 });
    await page.waitForTimeout(600);
    await s.ajaxIdle();
    s.note('jump results', await s.texts('#project-jump .drdn-items.projects a'));
    s.takeRequests();
    await page.locator('#project-jump .drdn-items.projects a').first().click();
    await page.waitForLoadState('load');
    s.noteURL('after jump');
    s.noteRequests('jump');
  },
  { readOnly: true },
);

// Headless Chromium does not dispatch access keys from synthetic key events, so activate the
// element the browser would pick for the key (the first one with that accesskey): inputs get
// the focus, other elements are clicked.
async function accessKey(s: Session, key: string) {
  const loc = s.page.locator(`[accesskey="${key}"]`).first();
  if ((await loc.count()) === 0) return `no element for ${key}`;
  const tag = await loc.evaluate((e) => e.tagName.toLowerCase());
  if (tag === 'input' || tag === 'textarea' || tag === 'select') {
    await loc.focus();
    return `focus ${tag}`;
  }
  const navigation = s.page.waitForNavigation({ waitUntil: 'load', timeout: 3000 }).then(() => true, () => false);
  await loc.evaluate((e) => (e as HTMLElement).click());
  const navigated = await navigation;
  return `click ${tag}${navigated ? ' (navigated)' : ''}`;
}

dualTest(
  'keyboard access keys',
  async (s) => {
    const { page } = s;
    await s.login('jsmith', 'jsmith');
    await page.goto('/projects/ecookbook');
    const list = async () =>
      page.locator('[accesskey]').evaluateAll((els) =>
        els.map((e) => `${e.getAttribute('accesskey')} ${e.tagName.toLowerCase()} ${e.getAttribute('href') ?? e.id}`));
    s.note('accesskeys on project overview', await list());
    s.note('7 (new issue)', await accessKey(s, '7'));
    s.noteURL('after 7');
    s.note('accesskeys on new issue', await list());
    s.note('f (quick search)', await accessKey(s, 'f'));
    s.note('focused after f', await page.evaluate(() => document.activeElement?.id));
    s.note('4 (search)', await accessKey(s, '4'));
    s.noteURL('after 4');
    await page.goto('/projects/ecookbook/issues');
    await page.goto('/issues/2');
    s.note('accesskeys on issue', await list());
    s.note('e (edit)', await accessKey(s, 'e'));
    s.note('edit form visible after e', await s.visible('#update'));
    s.note('n (next)', await accessKey(s, 'n'));
    s.noteURL('after n');
    s.note('p (previous)', await accessKey(s, 'p'));
    s.noteURL('after p');
    await page.goto('/projects/ecookbook/wiki');
    s.note('accesskeys on wiki', await list());
    s.note('e (wiki edit)', await accessKey(s, 'e'));
    s.noteURL('wiki edit via accesskey');
    s.note('accesskeys on wiki edit', await list());
    s.noteRequests('all');
  },
  { readOnly: true },
);
