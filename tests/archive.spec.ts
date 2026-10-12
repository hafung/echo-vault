import { test, expect } from '@playwright/test';

test('desktop navigation, sources, search, reader, copy, favorites and export', async ({ page, request }) => {
  const errors: string[] = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('/');
  await expect(page.locator('#all-count')).not.toHaveText('—');
  await expect(page.locator('.welcome h2')).toContainText('每一次对话');
  await expect(page.locator('.conversation-card').first()).toBeVisible();
  await page.screenshot({ path: 'artifacts/desktop-overview.png' });
  await page.getByRole('button', { name: '数据与来源' }).click();
  await expect(page.locator('#sources-dialog')).toBeVisible();
  await expect(page.locator('.source-row').filter({ hasText: 'Windows' })).toBeVisible();
  await expect(page.locator('.source-row').filter({ hasText: 'WSL' })).toBeVisible();
  await page.locator('#sources-dialog [data-close-dialog]').click();
  await page.locator('#global-search').fill('sqlite');
  await expect(page.locator('#search-summary')).toContainText('sqlite');
  await expect(page.locator('.conversation-card').first()).toBeVisible();
  await page.locator('.conversation-card').first().click();
  await expect(page.locator('.reader-header h2')).toBeVisible();
  await expect(page.locator('.message').first()).toBeVisible();
  await expect(page.locator('.prose mark').first()).toBeVisible();
  await page.screenshot({ path: 'artifacts/desktop-search-reader.png' });
  await page.locator('#session-search').click();
  await page.locator('#local-query').fill('sqlite');
  await expect(page.locator('#hit-count')).toContainText('/');
  await expect(page.locator('.hit-message')).toBeVisible();
  await page.locator('#close-local-search').click();
  const id = new URL(page.url()).searchParams.get('session')!;
  const before = await (await request.get('/api/sessions/' + encodeURIComponent(id))).json();
  await page.locator('#session-favorite').click();
  await expect(page.locator('#session-favorite')).toHaveClass(before.session.favorite ? /icon-button$/ : /is-favorite/);
  await page.locator('#session-favorite').click();
  await expect.poll(async () => (await (await request.get('/api/sessions/' + encodeURIComponent(id))).json()).session.favorite).toBe(before.session.favorite);
  await page.locator('#session-info').click();
  await expect(page.locator('#info-dialog')).toBeVisible();
  await expect(page.locator('.session-details')).toContainText(id);
  await page.locator('#info-dialog [data-close-dialog]').click();
  const exported = await request.get('/api/sessions/' + encodeURIComponent(id) + '/export?format=md');
  expect(exported.ok()).toBeTruthy(); expect(await exported.text()).toContain('# ');
  await page.locator('#theme').click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.screenshot({ path: 'artifacts/desktop-dark.png' });
  await page.locator('#theme').click();
  expect(errors).toEqual([]);
});

test('Chinese one and two character search, project filters, no-result and keyboard', async ({ page }) => {
  await page.goto('/');
  for (const q of ['中', '性能', '中文']) {
    await page.locator('#global-search').fill(q);
    await expect(page.locator('#search-summary')).toContainText(q);
    await expect(page.locator('.conversation-card').first()).toBeVisible();
  }
  await page.locator('#global-search').fill('unlikely_no_such_result_098761234');
  await expect(page.locator('.empty-list h3')).toContainText('没有找到');
  await page.keyboard.press('Escape');
  await expect(page.locator('#global-search')).toHaveValue('');
  await page.locator('.project-item').first().click();
  const project = await page.locator('#crumb').textContent();
  await expect(page.locator('.conversation-card').first().locator('.project-tag')).toContainText(project!);
  await page.keyboard.press('Control+k');
  await expect(page.locator('#global-search')).toBeFocused();
  await page.locator('#filter-button').click();
  await expect(page.locator('#filters')).toBeVisible();
  await page.locator('#platform-filter').selectOption('Windows');
  await page.getByRole('button', { name: '全部对话', exact: false }).first().click();
  await expect(page.locator('.conversation-card').first()).toContainText('Windows');
});

test('mobile reading, navigation and overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.locator('.conversation-card').first()).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.screenshot({ path: 'artifacts/mobile-list.png' });
  await page.locator('.conversation-card').first().click();
  await expect(page.locator('.reader-header h2')).toBeVisible();
  await page.screenshot({ path: 'artifacts/mobile-reader.png' });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await page.locator('#back-list').click();
  await expect(page.locator('.conversation-card').first()).toBeVisible();
  await page.locator('#mobile-menu').click();
  await expect(page.locator('.brand')).toBeVisible();
  await page.getByRole('button', { name: '数据与来源' }).click();
  await expect(page.locator('#sources-dialog')).toBeVisible();
  await page.screenshot({ path: 'artifacts/mobile-sources.png' });
});

test('search endpoints enforce same-origin writes', async ({ request }) => {
  const denied = await request.post('/api/sync', { data: {} }); expect(denied.status()).toBe(403);
  const refused = await request.post('/api/sync', { data: {}, headers: { 'X-Echo-Request': '1', Origin: 'http://evil.example' } }); expect(refused.status()).toBe(403);
  const result = await request.get('/api/sessions?q=' + encodeURIComponent('" OR 1=1 --'));
  expect(result.ok()).toBeTruthy(); expect((await result.json()).items).toHaveLength(0);
});

test('untrusted Markdown and remote images are removed from rendered conversations', async ({page}) => {
  await page.route('**/api/sessions/xss-fixture?*',route=>route.fulfill({json:{session:{id:'xss-fixture',title:'清理不可信 Markdown',project:'测试',provider:'Codex',platform:'WSL',model:'',favorite:0,message_count:1,total_count:1,created:1767225600,updated:1767225600},offset:0,total:1,more:false,items:[{id:999999,role:'assistant',kind:'message',phase:'',timestamp:1767225600,body:'# 安全文本\n\n<img src="https://evil.example/a" onerror="window.xss=1"><script>window.xss=1</script>\n\n[链接](javascript:alert(1))',metadata:{},truncated:false,length:100}]}}));
  await page.goto('/?session=xss-fixture');
  await expect(page.locator('.prose h1')).toHaveText('安全文本');
  await expect(page.locator('.prose script,.prose img,.prose [onerror],.prose a[href^="javascript:"]')).toHaveCount(0);
  expect(await page.evaluate(()=>('xss' in window))).toBeFalsy();
});

test('long reading stays bounded and preserves the visible conversation', async ({page,request}) => {
  const data=await(await request.get('/api/sessions?limit=60')).json();
  let session=data.items.find((s:{message_count:number})=>s.message_count>95);
  for(let offset=60; !session && offset<750; offset+=60) { const older=await(await request.get('/api/sessions?limit=60&offset='+offset)).json(); session=older.items.find((s:{message_count:number})=>s.message_count>95); if(!older.more)break; }
  expect(session,'应存在适合长阅读验收的真实对话').toBeTruthy();
  await page.goto('/?session='+encodeURIComponent(session.id));
  await expect(page.locator('.message')).toHaveCount(30);
  for (const count of [60,90,90]) {
    await page.locator('#next-messages').scrollIntoViewIfNeeded();
    await page.locator('#next-messages').click();
    await expect(page.locator('.message')).toHaveCount(count);
    await expect(page.locator('.reader-header h2')).toHaveText(session.title);
  }
  expect(await page.locator('.message').count()).toBeLessThanOrEqual(90);
});
