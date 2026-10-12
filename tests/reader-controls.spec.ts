import { test, expect, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

async function openReader(page: Page, count: number) {
  const session = { id: 'reader-controls', title: '阅读交互测试', project: '测试项目', provider: 'Codex', platform: 'WSL', model: '', created: 1, updated: 1, message_count: count, total_count: count + 10, favorite: 0, archived: 0, parent_id: null, preview: '搜索与滚动' };
  const messages = Array.from({ length: count + 10 }, (_, i) => ({ id: i + 1, role: i < count ? (i % 2 ? 'assistant' : 'user') : 'tool', kind: 'message', phase: '', timestamp: 1, body: `消息 ${i + 1}\n\n` + '这里是可搜索、可滚动的对话内容。\n\n'.repeat(4), truncated: false, length: 100, metadata: {} }));
  // Serve the built app and fixture API without touching the personal archive.
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/stats') return route.fulfill({ json: { sessions: 1, messages: count, events: count + 10, projects: 1, favorites: 0, projects_list: [{ project: '测试项目', count: 1 }], platforms: [], providers: [], last_sync: 1, db_bytes: 1, raw_bytes: 1 } });
    if (url.pathname === '/api/sync') return route.fulfill({ json: { running: false, done: 0, total: 0, errors: [], warnings: [] } });
    if (url.pathname === '/api/sessions') return route.fulfill({ json: { items: [session], more: false, offset: 1 } });
    if (url.pathname.endsWith('/hits')) return route.fulfill({ json: [{ id: count, role: 'assistant', preview: '可搜索' }] });
    if (url.pathname === '/api/sessions/reader-controls') {
      const items = url.searchParams.get('all') === '1' ? messages : messages.slice(0, count);
      const around = Number(url.searchParams.get('around'));
      const offset = around ? Math.max(0, items.findIndex(m => m.id === around) - 5) : Number(url.searchParams.get('offset'));
      return route.fulfill({ json: { session, items: items.slice(offset, offset + 30), total: items.length, offset, more: offset + 30 < items.length } });
    }
    if (url.pathname.startsWith('/api/')) throw new Error(`Unexpected API request: ${url.pathname}`);
    const asset = url.pathname.startsWith('/assets/') ? url.pathname.slice(1) : 'index.html';
    return route.fulfill({ body: await readFile(resolve('dist', asset)), contentType: asset.endsWith('.js') ? 'application/javascript' : asset.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  await page.goto('/?session=reader-controls');
  await expect(page.locator('.reader-header h2')).toHaveText(session.title);
}

async function expectEnd(page: Page, lastId: number) {
  await expect(page.locator('.message').last()).toHaveAttribute('id', `message-${lastId}`);
  await expect(page.locator('.conversation-end')).toBeInViewport();
  await expect.poll(() => page.locator('#message-scroll').evaluate(el => Math.abs(el.scrollHeight - el.clientHeight - el.scrollTop))).toBeLessThan(2);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
}

test('search toggles, clears matches and stays open through pagination with an empty query', async ({ page }) => {
  await openReader(page, 95);
  const toggle = page.locator('#session-search');
  await toggle.click();
  await expect(page.locator('#local-query')).toBeFocused();
  await toggle.click();
  await expect(page.locator('#local-search')).toBeHidden();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await page.locator('#go-end').click();
  await expectEnd(page, 95);
  await expect(page.locator('#local-search')).toBeVisible();
  await page.locator('#local-query').fill('可搜索');
  await expect(page.locator('#hit-count')).toHaveText('1 / 1');
  await expect(page.locator('.hit-message')).toHaveAttribute('id', 'message-95');
  await toggle.click();
  await expect(page.locator('#local-search')).toBeHidden();
  await expect(page.locator('.hit-message, .prose mark')).toHaveCount(0);
  await toggle.click();
  await expect(page.locator('#local-query')).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(page.locator('#local-search')).toBeHidden();
  await page.keyboard.press('Control+f');
  await page.keyboard.press('Control+f');
  await expect(page.locator('#local-query')).toBeFocused();
  await page.locator('#close-local-search').click();
  await expect(page.locator('#local-search')).toBeHidden();
});

for (const count of [8, 95]) {
  test(`start and end scroll to the actual boundaries of a ${count}-message conversation`, async ({ page }) => {
    await openReader(page, count);
    await page.locator('#go-end').click();
    await expectEnd(page, count);
    await page.locator('#go-start').click();
    await expect(page.locator('.message').first()).toHaveAttribute('id', 'message-1');
    await expect.poll(() => page.locator('#message-scroll').evaluate(el => el.scrollTop)).toBe(0);
    await page.locator('#go-end').click();
    await expectEnd(page, count);
    await page.locator('#show-all').check();
    await expect(page.locator('.reader-footer')).toContainText(`/ ${count + 10} 条`);
    await page.locator('#go-end').click();
    await expectEnd(page, count + 10);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('#go-end').click();
    await expectEnd(page, count + 10);
    for (const viewport of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
      await page.setViewportSize(viewport);
      const body = await page.locator('.reader-body').boundingBox();
      const nav = await page.locator('.reader-navigation').boundingBox();
      expect(nav!.x).toBeGreaterThan(body!.x);
      expect(nav!.y).toBeGreaterThan(body!.y);
      expect(nav!.x + nav!.width).toBeLessThan(body!.x + body!.width);
      expect(nav!.y + nav!.height).toBeLessThan(body!.y + body!.height);
      await page.screenshot({ path: `artifacts/reader-controls-${count}-${viewport.width}.png` });
      await page.locator('#theme').click();
    }
  });
}
