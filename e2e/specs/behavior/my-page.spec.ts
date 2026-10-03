import { dualTest } from '../../lib/dual';
import { dragWithMouse, ids } from '../../lib/ui';

dualTest('my page: add, remove and reorder blocks (drag & drop)', async (s) => {
  const { page } = s;
  await s.login('admin', 'admin');
  await page.goto('/my/page');
  s.takeRequests();
  s.note('blocks', {
    top: await ids(page, '#list-top > div.mypage-box'),
    left: await ids(page, '#list-left > div.mypage-box'),
    right: await ids(page, '#list-right > div.mypage-box'),
  });
  s.note('block select options', await page.locator('#block-select option').evaluateAll((os) =>
    os.map((o) => `${(o as HTMLOptionElement).value}${(o as HTMLOptionElement).disabled ? ' (disabled)' : ''}`)));

  // Adding a block submits #block-form remotely and inserts the block at the top.
  await page.selectOption('#block-select', 'news');
  await s.ajaxIdle();
  s.noteRequests('add block');
  s.note('top after add', await ids(page, '#list-top > div.mypage-box'));
  s.note('block select options after add', await page.locator('#block-select option').evaluateAll((os) =>
    os.map((o) => `${(o as HTMLOptionElement).value}${(o as HTMLOptionElement).disabled ? ' (disabled)' : ''}`)));
  s.note('select reset', await page.locator('#block-select').inputValue());
  await s.noteText('news block title', '#block-news h3');

  // Remove a block (data-remote link).
  await page.click('#block-documents a.icon-close');
  await s.ajaxIdle();
  s.noteRequests('remove block');
  s.note('documents removed', (await page.locator('#block-documents').count()) === 0);
  s.note('block select options after remove', await page.locator('#block-select option').evaluateAll((os) =>
    os.map((o) => `${(o as HTMLOptionElement).value}${(o as HTMLOptionElement).disabled ? ' (disabled)' : ''}`)));

  // Drag the news block from the top list into the right list.
  const before = await ids(page, '#list-right > div.mypage-box');
  await dragWithMouse(page, page.locator('#block-news .sort-handle'), page.locator(`#${before[0]}`), { below: true });
  await s.ajaxIdle();
  s.noteRequests('drag between lists');
  s.note('after drag', {
    top: await ids(page, '#list-top > div.mypage-box'),
    right: await ids(page, '#list-right > div.mypage-box'),
  });

  // Reload: the order persisted server side.
  await page.reload();
  s.note('after reload', {
    top: await ids(page, '#list-top > div.mypage-box'),
    left: await ids(page, '#list-left > div.mypage-box'),
    right: await ids(page, '#list-right > div.mypage-box'),
  });
  s.takeRequests();

  // Block settings (the issue query block has an inline settings form toggled by JS).
  await page.selectOption('#block-select', 'issuequery');
  await s.ajaxIdle();
  s.note('issuequery settings visible', await s.visible('#block-issuequery form'));
  s.noteRequests('add issuequery');
});
