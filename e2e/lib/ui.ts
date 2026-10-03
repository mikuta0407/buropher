// Small UI helpers shared by the behavior specs.
import { Locator, Page } from '@playwright/test';

/**
 * Drags `handle` onto `target` with intermediate mouse moves, which is what jQuery UI
 * sortable needs (it ignores a single synthetic drop event).
 */
export async function dragWithMouse(page: Page, handle: Locator, target: Locator, opts: { below?: boolean } = {}) {
  const hb = await handle.boundingBox();
  const tb = await target.boundingBox();
  if (!hb || !tb) throw new Error('drag: element not visible');
  const sx = hb.x + hb.width / 2;
  const sy = hb.y + hb.height / 2;
  const tx = tb.x + tb.width / 2;
  const ty = opts.below ? tb.y + tb.height - 2 : tb.y + 2;
  await page.mouse.move(sx, sy);
  await page.mouse.down();
  // A small first move starts the sortable (distance threshold), then glide to the target.
  await page.mouse.move(sx + 5, sy + 5, { steps: 3 });
  await page.mouse.move(tx, ty, { steps: 20 });
  await page.mouse.move(tx, ty + (opts.below ? 4 : -2), { steps: 3 });
  await page.mouse.up();
}

/** Returns the ids of the elements matching selector, in document order. */
export async function ids(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).evaluateAll((els) => els.map((e) => e.id));
}
