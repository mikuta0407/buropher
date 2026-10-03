import { dualTest, Session } from '../../lib/dual';

async function noteList(s: Session, key: string) {
  const { page } = s;
  s.note(`${key}: columns`, await s.texts('table.issues thead th'));
  s.note(
    `${key}: rows`,
    await page.locator('table.issues tbody tr.issue').evaluateAll((trs) => trs.map((tr) => tr.id)),
  );
  s.note(`${key}: groups`, await s.texts('table.issues tbody tr.group'));
  s.note(`${key}: totals`, await s.text('p.query-totals'));
  s.note(`${key}: count`, await s.text('span.pagination .items'));
}

dualTest(
  'issues list: filters, operators, columns, group by, totals',
  async (s) => {
    const { page } = s;
    await s.login('admin', 'admin');
    await page.goto('/projects/ecookbook/issues');
    s.takeRequests();
    await noteList(s, 'initial');

    // Add the "Tracker" filter through the select box; this builds the row client side.
    await page.selectOption('#add_filter_select', 'tracker_id');
    s.note('tracker row visible', await s.visible('#tr_tracker_id'));
    s.note('tracker operators', await page.locator('#operators_tracker_id option').allTextContents());
    s.note('tracker values', await page.locator('#values_tracker_id_1 option').allTextContents());
    await page.selectOption('#values_tracker_id_1', '1');

    // Change the status operator to "closed": the value select must disappear.
    await page.selectOption('#operators_status_id', 'c');
    s.note('status values hidden for closed', !(await s.visible('#values_status_id_1')));
    await page.selectOption('#operators_status_id', '*');

    // Remote filters: the values are fetched from /queries/filter by XHR when the filter is added.
    await page.selectOption('#add_filter_select', 'assigned_to_id');
    await s.ajaxIdle();
    s.note('assignee values (remote)', await page.locator('#values_assigned_to_id_1 option').allTextContents());
    await page.click('#cb_assigned_to_id');
    await page.selectOption('#add_filter_select', 'cf_1');
    await s.ajaxIdle();
    s.note('custom field values (remote)', await page.locator('#values_cf_1_1 option').allTextContents());
    await page.click('#cb_cf_1');
    s.note('unchecking hides operators', !(await s.visible('#operators_cf_1')));

    await page.selectOption('#add_filter_select', 'subject');
    await page.selectOption('#operators_subject', '~');
    await page.fill('#values_subject', 'e');
    s.noteRequests('building filters');

    // Options: columns, group by, totals.
    await page.click('#options legend');
    s.note('options expanded', await s.visible('#available_c'));
    await page.selectOption('#available_c', ['start_date', 'estimated_hours']);
    await page.click('#query_form .query-columns + .buttons button.move-right');
    await page.selectOption('#selected_c', 'start_date');
    await page.click('#query_form button[onclick^="moveOptionTop"]');
    s.note('selected columns', await page.locator('#selected_c option').allTextContents());
    await page.selectOption('#group_by', 'status');
    await page.check('#query_form input[name="t[]"][value="estimated_hours"]');

    await page.click('#query_form p.buttons a.icon-checked');
    await page.waitForLoadState('load');
    s.noteURL('applied');
    s.noteRequests('apply');
    await noteList(s, 'applied');
    s.note('filters collapsed state', await page.locator('#filters').getAttribute('class'));

    // Collapse a group and expand it again (client side toggle).
    const toggle = page.locator('table.issues tr.group span.expander').first();
    if ((await toggle.count()) > 0) {
      await toggle.click();
      s.note('group collapsed: visible issue rows', await page.locator('table.issues tbody tr.issue:visible').count());
      await toggle.click();
      s.note('group expanded: visible issue rows', await page.locator('table.issues tbody tr.issue:visible').count());
    }

    // Sorting by clicking a column header.
    await page.click('table.issues thead th a:has-text("Start date")');
    await page.waitForLoadState('load');
    s.noteURL('sorted');
    await noteList(s, 'sorted');

    // Clear resets to the default query.
    await page.click('#query_form p.buttons a.icon-reload');
    await page.waitForLoadState('load');
    s.noteURL('cleared');
    await noteList(s, 'cleared');
    s.noteRequests('sort and clear');
  },
);

dualTest(
  'issues list: save query via form, select it from sidebar',
  async (s) => {
    const { page } = s;
    await s.login('admin', 'admin');
    await page.goto('/projects/ecookbook/issues?set_filter=1&f[]=status_id&op[status_id]=o&f[]=priority_id&op[priority_id]==&v[priority_id][]=4');
    await noteList(s, 'filtered');
    s.takeRequests();
    await page.click('#query_form p.buttons a.icon-save');
    await page.waitForLoadState('load');
    s.noteURL('new query form');
    s.note('filter rows carried over', await page.locator('#filters-table tr.filter').evaluateAll((trs) => trs.map((tr) => tr.id)));
    await page.fill('#query_name', 'E2E query');
    await page.check('#query_visibility_2');
    await page.click('#query-form input[type=submit], form#query-form [type=submit]');
    await page.waitForLoadState('load');
    s.noteURL('saved');
    s.noteRequests('save');
    await s.noteText('title', '#content h2');
    await noteList(s, 'saved query');
    await s.noteTexts('sidebar queries', '#sidebar .queries a');
  },
);
