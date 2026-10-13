import { test, expect } from '@playwright/test';

const session = { id: 'dil-fixture', title: 'Intelligent UI 归档', project: 'ChatGPT', cwd: '', provider: 'ChatGPT', platform: 'Windows', model: 'GPT', favorite: 0, message_count: 1, total_count: 1, created: 1767225600, updated: 1767225600 };

async function fixture(page: import('@playwright/test').Page, body: string, metadata = {}, truncated = false, expanded = body) {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/stats') return route.fulfill({ json: { sessions: 1, messages: 1, events: 1, projects: 1, favorites: 0, projects_list: [], platforms: [], providers: [], last_sync: 1, db_bytes: 0, raw_bytes: 0 } });
    if (path === '/api/messages/1') return route.fulfill({ json: { body: expanded } });
    if (path === '/api/sessions/dil-fixture') return route.fulfill({ json: { session, offset: 0, total: 1, more: false, items: [{ id: 1, role: 'assistant', kind: 'message', phase: '', timestamp: session.created, body, metadata, truncated, length: 25000 }] } });
    if (path === '/api/sessions') return route.fulfill({ json: { items: [], more: false, offset: 0, elapsed_ms: 0 } });
    if (path === '/api/sync') return route.fulfill({ json: { running: false, started: 1, finished: 1, errors: [], warnings: [], done: 0, total: 0, imported: 0, skipped: 0 } });
    return route.fulfill({ json: {} });
  });
  await page.goto('/?session=dil-fixture');
  await expect(page.locator('.prose')).toBeVisible();
}

test('nested DIL keeps prose, inline entities, links, citations, loops and tables', async ({ page }) => {
  const body = `# 推荐\n\n<box border padding={3}>\n  **第二选择：**\n\n  如果核心功能是声音克隆、AI 角色配音，特别是克隆声音之后还能控制人物的语气情绪，那么我甚至会反过来优先选它。\n\n  **<Entity value="声音平台"/>**，<Link url="https://example.com/voice" title="官方网站"/>。<Cite refs={["turn1search0"]}/>\n  <grid columns={2}>\n    {#each [{name:"声音克隆",desc:"角色配音"},{name:"情绪控制",desc:"语气调整"}] as item, i}\n      <grid-item>\n        <text>{item.name}</text>\n        <caption>{item.desc}</caption>\n      </grid-item>\n    {/each}\n  </grid>\n  <table><table-row><table-cell header>功能</table-cell><table-cell>保留内容</table-cell></table-row></table>\n</box>\n\n普通 **Markdown** 与 [外链](https://example.com)。\n\n\`\`\`xml\n<box>代码内的组件不应解析</box>\n\`\`\`\n\n行内代码 \`<Entity value="原样保留"/>\`。`;
  await fixture(page, body, { chatgpt_ui: { model_dil_v2: { appData: { opGenui: { componentResults: { cite: { componentName: 'Cite', state: { items: [{ url: 'https://example.com/source', title: '来源页面' }] } } } } } } } });
  const prose = page.locator('.dil-response');
  await expect(prose.locator('strong').filter({ hasText: '第二选择' })).toBeVisible();
  await expect(prose).toContainText('那么我甚至会反过来优先选它');
  await expect(prose.locator('.dil-entity')).toHaveText('声音平台');
  await expect(prose.locator('a[href="https://example.com/voice"]')).toHaveText('官方网站');
  await expect(prose.locator('.dil-citations a')).toHaveAttribute('href', 'https://example.com/source');
  await expect(prose.locator('.dil-grid-item')).toHaveCount(2);
  await expect(prose.locator('.dil-grid-item').last()).toContainText('语气调整');
  await expect(prose.locator('th')).toHaveText('功能');
  await expect(prose.locator('td')).toHaveText('保留内容');
  await expect(prose.locator('pre code')).toHaveText('<box>代码内的组件不应解析</box>\n');
  await expect(prose.locator('p code').last()).toHaveText('<Entity value="原样保留"/>');
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
});

test('resolved images use compiled resolution order, load on click and survive expansion', async ({ page }) => {
  let imageRequests = 0;
  await page.route('https://images.example/**', route => { imageRequests++; return route.fulfill({ contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aM1sAAAAASUVORK5CYII=', 'base64') }); });
  const body = '<box>\n  <AsyncImage query="第一张"/>\n  <AsyncImage query="第二张"/>\n</box>';
  const metadata = { chatgpt_ui: { model_dil_v2: { code: '__dil.jsx(AsyncImage,{"__resolutionId":__dilConstants["0"]});__dil.jsx(AsyncImage,{"__resolutionId":__dilConstants["1"]})', constants: { '0': 'z-first', '1': 'a-second' }, appData: { opGenui: { componentResults: { 'a-second': { componentName: 'AsyncImage', state: { images: [{ title: '第二张图', content_url: 'https://images.example/second.png' }] } }, 'z-first': { componentName: 'AsyncImage', state: { images: [{ title: '第一张图', content_url: 'https://images.example/first.png' }] } } } } } } } };
  await fixture(page, body, metadata, true, body + '\n\n完整回答的后半段。');
  await expect(page.locator('.dil-image figcaption').first()).toHaveText('第一张图');
  await expect(page.locator('.dil-image figcaption').last()).toHaveText('第二张图');
  expect(imageRequests).toBe(0);
  await page.locator('.dil-load-image').first().click();
  await expect(page.locator('.dil-image img')).toHaveAttribute('src', 'https://images.example/first.png');
  await expect.poll(() => imageRequests).toBe(1);
  await page.locator('.expand-message').click();
  await expect(page.locator('.dil-response')).toContainText('完整回答的后半段');
  await expect(page.locator('.dil-load-image')).toHaveCount(2);
});

test('static state and conditionals render without running JavaScript', async ({ page }) => {
  await fixture(page, '{@body const [seats,setSeats] = DIL.useState(8)}\n{@body const price = seats*29}\n<box><title>${price}/mo</title>{#if seats > 5}<text>团队方案</text>{:else}<text>个人方案</text>{/if}<slider value={seats}/><button onClick={()=>{window.xss=1}}>确认</button></box>');
  await expect(page.locator('.dil-title')).toHaveText('$232/mo');
  await expect(page.locator('.dil-response')).toContainText('团队方案');
  await expect(page.locator('.dil-response')).not.toContainText('个人方案');
  await expect(page.locator('.dil-static-control').first()).toContainText('8');
  expect(await page.evaluate(() => 'xss' in window)).toBeFalsy();
});

test('SVG diagrams preserve their text and following paragraphs', async ({ page }) => {
  await fixture(page, '<box>\n  <svg viewBox="0 0 340 84" width="100%">\n    {#each [{x:6,t:"男主参考"},{x:119,t:"女主参考"}] as v,i}\n      <rect x={v.x} y="4" width="102" height="29" fill="#64748b"/>\n      <text x={v.x+51} y="23">{v.t}</text>\n      {#if i<1}<path d={`M${v.x+94} 27 h22`} stroke="currentColor"/>{/if}\n    {/each}\n  </svg>\n  图片负责人物和场景一致性，文字决定新的画面构图和镜头运动。\n</box>');
  await expect(page.locator('.dil-response svg text')).toHaveText(['男主参考', '女主参考']);
  await expect(page.locator('.dil-response svg path')).toHaveAttribute('d', 'M100 27 h22');
  await expect(page.locator('.dil-response')).toContainText('图片负责人物和场景一致性');
});

test('ordinary Markdown, partial DIL and hostile markup stay readable and sanitized', async ({ page }) => {
  const body = '# 普通 Markdown\n\n**强调**\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```html\n<box>原样</box>\n```\n\n<script>window.xss=1</script><img src="https://evil.example/a" onerror="window.xss=1">\n\n[危险](javascript:alert(1))';
  await fixture(page, body);
  await expect(page.locator('.prose h1')).toHaveText('普通 Markdown');
  await expect(page.locator('.prose strong')).toHaveText('强调');
  await expect(page.locator('.prose table')).toContainText('2');
  await expect(page.locator('.prose pre code')).toHaveText('<box>原样</box>\n');
  await expect(page.locator('.prose img,.prose script,.prose [onerror],.prose a[href^="javascript:"]')).toHaveCount(0);
  expect(await page.evaluate(() => 'xss' in window)).toBeFalsy();
  await page.unroute('**/api/**');
  await fixture(page, '<box>\n  **尚未闭合的回答**\n\n  正文仍然可读。\n  <AsyncImage query="未保存的图片"/>');
  await expect(page.locator('.dil-response strong')).toHaveText('尚未闭合的回答');
  await expect(page.locator('.dil-response')).toContainText('正文仍然可读');
  await expect(page.locator('.dil-image')).toContainText('图片未包含在存档中');
});
