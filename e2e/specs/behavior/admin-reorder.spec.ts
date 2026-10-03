import { dualTest, Session } from '../../lib/dual';
import { dragWithMouse } from '../../lib/ui';

async function rowNames(s: Session, selector: string) {
  return (await s.page.locator(selector).allInnerTexts()).map((t) => t.split('\t')[0].trim());
}

for (const [label, url, rows] of [
  ['trackers', '/trackers', 'table.trackers tbody tr td.name'],
  ['issue statuses', '/issue_statuses', 'table.list tbody tr td.name'],
  ['roles', '/roles', 'table.roles tbody tr td.name'],
  ['enumerations (priorities)', '/enumerations', 'table.list:nth-of-type(2) tbody tr td.name'],
] as const) {
  dualTest(`admin: reorder ${label} with the drag handle`, async (s) => {
    const { page } = s;
    await s.login('admin', 'admin');
    await page.goto(url);
    s.takeRequests();
    s.note('rows before', await rowNames(s, rows));
    const handles = page.locator(rows.replace(/ td\.name$/, '') + ' .sort-handle');
    const n = await handles.count();
    s.note('handles', n);
    if (n < 3) return;
    // Move the first row below the third.
    const thirdRow = handles.nth(2).locator('xpath=ancestor::tr');
    await dragWithMouse(page, handles.nth(0), thirdRow, { below: true });
    await s.ajaxIdle();
    s.noteRequests('drag');
    s.note('rows after drag', await rowNames(s, rows));
    await page.reload();
    s.note('rows after reload', await rowNames(s, rows));
    await s.noteText('flash after reload', '#flash_notice, #flash_error');
  });
}
