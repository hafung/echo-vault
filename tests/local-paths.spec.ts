import { test, expect, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const examplePath = 'D:/projects/codex/2026-10-06/three-js-imagegen-skill-3d-tts/outputs/pip-and-the-little-star/dist/illustrations/prompts-journey.md';
const body = [
  `[Windows 文件](${examplePath}:12)`,
  `http://localhost:8765/${examplePath}`,
  '[带空格文件](http://127.0.0.1:8765/D:/notes/%E4%B8%AD%E6%96%87%20file.md)',
  '[文件协议](file:///D:/notes/file.md)',
  '[WSL 文件](/home/zz/notes.md#L8)',
  '[相对文件](outputs/notes.md)',
  '`D:\\projects\\我的文件夹`',
  '本地文本路径 /home/zz/report.md，和 D:/projects/notes.md。',
  '[外部网址](https://example.com/D:/notes/file.md)',
  '[本地网页](http://localhost:3000/dashboard)',
  '```sh\ncat /home/zz/notes.md\n```',
].join('\n\n');

async function fixture(page: Page) {
  const calls: Record<string, unknown>[] = [];
  let fail = false;
  const session = { id: 'local-paths', title: '本地路径测试', project: '测试项目', cwd: 'D:/projects/demo', provider: 'Codex', platform: 'Windows', model: '', created: 1, updated: 1, message_count: 1, total_count: 1, favorite: 0, archived: 0, parent_id: null, preview: '路径' };
  await page.context().route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.hostname === 'example.com') return route.fulfill({ contentType: 'text/html', body: 'External page' });
    if (url.pathname === '/api/stats') return route.fulfill({ json: { sessions: 1, messages: 1, events: 1, projects: 1, favorites: 0, projects_list: [], platforms: [], providers: [] } });
    if (url.pathname === '/api/sync') return route.fulfill({ json: { running: false, errors: [], warnings: [] } });
    if (url.pathname === '/api/sessions') return route.fulfill({ json: { items: [session], more: false, offset: 1 } });
    if (url.pathname === '/api/sessions/local-paths') return route.fulfill({ json: { session, items: [{ id: 1, role: 'assistant', kind: 'message', phase: '', timestamp: 1, body, truncated: true, length: body.length + 30, metadata: {} }], total: 1, offset: 0, more: false } });
    if (url.pathname === '/api/messages/1') return route.fulfill({ json: { body: body + '\n\n[展开后的文件](D:/notes/full.md)' } });
    if (url.pathname.endsWith('/hits')) return route.fulfill({ json: [{ id: 1, role: 'assistant', preview: 'prompts-journey' }] });
    if (url.pathname === '/api/local/open') {
      calls.push(route.request().postDataJSON());
      return route.fulfill({ status: fail ? 400 : 200, json: fail ? { error: '本地文件或文件夹不存在，可能已移动或删除' } : { ok: true } });
    }
    if (url.pathname.startsWith('/api/')) throw new Error(`Unexpected API: ${url.pathname}`);
    const asset = url.pathname.startsWith('/assets/') ? url.pathname.slice(1) : 'index.html';
    return route.fulfill({ body: await readFile(resolve('dist', asset)), contentType: asset.endsWith('.js') ? 'application/javascript' : asset.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  await page.goto('/?session=local-paths');
  await expect(page.locator('.reader-header h2')).toHaveText(session.title);
  return { calls, fail: () => { fail = true; } };
}

test('local destinations are file controls while external URLs remain direct links', async ({ page }) => {
  const { calls } = await fixture(page);
  const prose = page.locator('.prose');
  await expect(prose.locator('.local-path-link')).toHaveCount(9);
  await expect(prose.getByRole('button', { name: 'Windows 文件', exact: true })).toHaveAttribute('data-local-path', examplePath);
  await expect(prose.getByRole('button', { name: '带空格文件', exact: true })).toHaveAttribute('data-local-path', 'D:/notes/中文 file.md');
  await expect(prose.getByRole('button', { name: '文件协议', exact: true })).toHaveAttribute('data-local-path', 'D:/notes/file.md');
  await expect(prose.getByRole('button', { name: 'WSL 文件', exact: true })).toHaveAttribute('data-local-path', '/home/zz/notes.md');
  await expect(prose.getByRole('button', { name: '相对文件', exact: true })).toHaveAttribute('data-local-path', 'D:/projects/demo/outputs/notes.md');
  await expect(prose.locator('pre .local-path-link')).toHaveCount(0);
  await expect(prose.getByRole('link', { name: '外部网址' })).toHaveAttribute('href', 'https://example.com/D:/notes/file.md');
  await expect(prose.getByRole('link', { name: '外部网址' })).toHaveAttribute('target', '_blank');
  await expect(prose.getByRole('link', { name: '本地网页' })).toHaveAttribute('href', 'http://localhost:3000/dashboard');
  expect(calls).toHaveLength(0);
  const popupPromise = page.waitForEvent('popup');
  await prose.getByRole('link', { name: '外部网址' }).click();
  const popup = await popupPromise;
  await expect(popup).toHaveURL('https://example.com/D:/notes/file.md');
  await expect(page.locator('#local-path-dialog')).toBeHidden();
  await popup.close();
});

test('localhost drive paths require confirmation and support opening the containing folder', async ({ page }) => {
  const { calls } = await fixture(page);
  const original = page.url();
  const link = page.getByRole('button', { name: `http://localhost:8765/${examplePath}`, exact: true });
  await link.click();
  await expect(page.locator('#local-path-value')).toHaveText(examplePath);
  expect(calls).toHaveLength(0);
  await page.locator('#local-path-cancel').click();
  expect(calls).toHaveLength(0);
  expect(page.url()).toBe(original);
  await link.click();
  await page.locator('#local-path-open').click();
  await expect(page.locator('#local-path-dialog')).toBeHidden();
  expect(calls).toEqual([{ path: examplePath, action: 'open', confirmed: true }]);
  await page.getByRole('button', { name: 'D:\\projects\\我的文件夹', exact: true }).click();
  await page.locator('#local-path-reveal').click();
  await expect(page.locator('#local-path-dialog')).toBeHidden();
  expect(calls[1]).toEqual({ path: 'D:\\projects\\我的文件夹', action: 'reveal', confirmed: true });
});

test('missing files show a retryable error; expanded and searched paths keep their action', async ({ page }) => {
  const control = await fixture(page);
  await page.locator('.expand-message').click();
  await page.getByRole('button', { name: '展开后的文件', exact: true }).click();
  await page.keyboard.press('Escape');
  await page.locator('#session-search').click();
  await page.locator('#local-query').fill('prompts-journey');
  await expect(page.locator('.local-path-link mark').first()).toHaveText('prompts-journey');
  await page.getByRole('button', { name: 'Windows 文件', exact: true }).click();
  control.fail();
  await page.locator('#local-path-open').click();
  await expect(page.locator('#local-path-error')).toContainText('不存在');
  await expect(page.locator('#local-path-dialog')).toBeVisible();
  await expect(page.locator('#local-path-open')).toBeEnabled();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#local-path-open')).toBeInViewport();
  expect(await page.locator('#local-path-dialog').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
});
