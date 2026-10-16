// Public README screenshots use only this fictional fixture and built assets.
// Every browser request is intercepted; no app server or personal archive is read.
import { chromium, expect } from '@playwright/test';
import { existsSync } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = join(root, 'docs/images');
const origin = 'http://gpt-vault.demo';
const time = new Date('2026-06-18T09:30:00Z');
const timestamp = time.getTime() / 1000;
const message = (id, role, body) => ({
  id, role, body, kind: 'text', phase: '', timestamp: timestamp - 600 + id,
  length: body.length, truncated: false, metadata: {},
});
const archiveMessages = [
  message(1, 'user', '我想把 ChatGPT 的方案讨论和 Codex 的开发记录放在一起，之后能按关键词找回答案。应该怎样组织？'),
  message(2, 'assistant', `## 先保留完整对话，再建立轻量索引

按项目归档，保留提问、回答和代码，让每个结论都有上下文。

| 来源 | 适合保留的内容 | 找回方式 |
| --- | --- | --- |
| ChatGPT | 方案讨论、学习笔记、研究结论 | 标题与正文搜索 |
| Codex · Windows | 本机开发记录、调试过程 | 项目筛选 |
| Codex · WSL | 实现细节、命令与验证结果 | 项目 + 关键词 |

### 一个档案，三种找回方式

- **按项目浏览**：把同一个应用的讨论和实现放在一起。
- **全文搜索**：忘了标题，也能通过回答中的关键词找到它。
- **收藏重要结论**：留下值得反复查看的解释、方案和代码。

> 对话正文与备份保存在本地。换电脑时，带走完整档案就能继续阅读。`),
];
const searchMessages = [
  message(11, 'user', '给笔记应用加入全文搜索：支持中文关键词，在结果和正文中高亮命中内容。先说明实现步骤。'),
  message(12, 'assistant', `## 把搜索范围限定为标题与正文

使用 SQLite 保存笔记和全文索引。先保证中文短词能找到，再考虑排序和分页。

### 实现顺序

1. 保存原始正文，为全文搜索建立独立索引。
2. 多个关键词取交集，双引号表示精确短语。
3. 返回匹配片段，并在打开笔记后定位命中位置。

\`\`\`ts
const params = new URLSearchParams({
  q: query.trim(),
  project: activeProject,
  offset: String(offset),
});
const results = await fetch('/api/notes?' + params);
\`\`\`

### 验证重点

| 场景 | 预期结果 |
| --- | --- |
| 中文短词搜索 | 标题和正文都能命中 |
| 多个关键词 | 只返回同时包含这些词的笔记 |
| 打开搜索结果 | 自动定位并高亮对应段落 |

索引可以重新生成；原始正文始终单独保留。`),
];
const samples = [
  ['chatgpt:demo-archive', '把对话整理成个人知识库', 'ChatGPT', 'ChatGPT', 'Windows', archiveMessages, 1],
  ['demo-search', '给笔记应用加入全文搜索', 'atlas-notes', 'Codex', 'WSL', searchMessages, 1],
  ['demo-weather', '完善天气卡片的加载与重试状态', 'weather-board', 'Codex', 'Windows', [message(21, 'user', '为天气卡片设计加载、空数据和重试状态。'), message(22, 'assistant', '先列出加载中、已完成和可重试错误三种状态，再为每种状态提供清楚的界面提示。')], 0],
  ['chatgpt:demo-reading', '制定每周阅读与回顾计划', 'ChatGPT', 'ChatGPT', 'Windows', [message(31, 'user', '帮我设计一个容易坚持的阅读计划。'), message(32, 'assistant', '每天留出二十分钟阅读，周末整理三条笔记，并在下周开始前回顾。')], 0],
  ['demo-highlight', '为搜索结果添加片段与高亮', 'atlas-notes', 'Codex', 'WSL', [message(41, 'user', '搜索结果需要展示匹配片段。'), message(42, 'assistant', '从命中位置提取上下文片段，保留完整正文；高亮只作用于显示内容。')], 0],
  ['demo-journal', '实现每周习惯回顾页面', 'habit-journal', 'Codex', 'Windows', [message(51, 'user', '展示本周记录与完成天数。'), message(52, 'assistant', '用日历概览和每周汇总展示完成情况，并允许回看每天的记录。')], 0],
  ['chatgpt:demo-backup', '梳理本地档案的备份与恢复', 'ChatGPT', 'ChatGPT', 'Windows', [message(61, 'user', '怎样让本地档案方便备份和迁移？'), message(62, 'assistant', '生成一致的完整快照，保留正文、收藏、附件和搜索索引；恢复时写入独立文件。')], 1],
  ['demo-weather-layout', '调整桌面与手机天气布局', 'weather-board', 'Codex', 'WSL', [message(71, 'user', '让天气页面适配手机屏幕。'), message(72, 'assistant', '宽屏并排显示预报卡片，小屏改为单列，并保留温度和更新时间。')], 0],
  ['demo-filters', '组合项目筛选与关键词搜索', 'atlas-notes', 'Codex', 'WSL', [message(81, 'user', '搜索时允许限定项目。'), message(82, 'assistant', '把项目与关键词作为独立筛选条件，清除项目时恢复全局搜索。')], 0],
  ['demo-journal-export', '导出月度习惯记录', 'habit-journal', 'Codex', 'Windows', [message(91, 'user', '增加月度记录导出。'), message(92, 'assistant', '导出包含日期、习惯名称和完成状态，保留稳定的列名和日期格式。')], 0],
];
const conversations = new Map();
const sessions = samples.map(([id, title, project, provider, platform, messages, favorite], index) => {
  conversations.set(id, messages);
  return {
    id, title, project, provider, platform, favorite, model: '',
    cwd: provider === 'Codex' ? '/home/demo/projects/' + project : '',
    created: timestamp - 86400, updated: timestamp - 300 - index * 3600,
    archived: 0, hidden: 0, project_hidden: 0, parent_id: null,
    message_count: messages.length, total_count: messages.length,
    preview: messages[0].body, metadata: { desktop_cache: provider === 'ChatGPT' },
  };
});
const projects = [...new Set(sessions.map(s => s.project))].map(project => ({
  project, count: sessions.filter(s => s.project === project).length,
  updated: Math.max(...sessions.filter(s => s.project === project).map(s => s.updated)), hidden: 0,
}));
const stats = {
  sessions: sessions.length, messages: sessions.reduce((n, s) => n + s.message_count, 0),
  events: sessions.reduce((n, s) => n + s.total_count, 0), projects: projects.length,
  favorites: sessions.filter(s => s.favorite).length, projects_list: projects,
  platforms: ['Windows', 'WSL'].map(platform => ({platform, count: sessions.filter(s => s.platform === platform).length})),
  providers: ['Codex', 'ChatGPT'].map(provider => ({provider, count: sessions.filter(s => s.provider === provider).length})),
  last_sync: timestamp - 60, db_bytes: 48 * 1024 ** 2, raw_bytes: 32 * 1024 ** 2,
};

const cachedChrome = join(homedir(), '.cache/ms-playwright/chromium-1228/chrome-linux64/chrome');
const executablePath = process.env.GPT_VAULT_CHROMIUM || (existsSync(cachedChrome) ? cachedChrome : undefined);
await mkdir(output, {recursive: true});
const browser = await chromium.launch({executablePath});
const failures = [];
let showBackup = false;
try {
  const context = await browser.newContext({viewport: {width: 1440, height: 1080}, deviceScaleFactor: 1, locale: 'zh-CN', timezoneId: 'Asia/Shanghai', serviceWorkers: 'block'});
  // Route the whole context, including any future popup or image request.
  await context.route('**/*', async route => {
    const url = new URL(route.request().url());
    const send = json => route.fulfill({json});
    if (url.origin !== origin || route.request().method() !== 'GET') {
      failures.push('Blocked unexpected request: ' + url.pathname);
      return route.abort();
    }
    if (url.pathname === '/api/stats') return send(stats);
    if (url.pathname === '/api/sync') return send({running: false, automatic: true, stage: 'done', done: 10, total: 10, imported: 20, skipped: 0, errors: [], warnings: [], file: '', started: timestamp - 62, finished: timestamp - 60, duration: 2, desktop_captured: 3, desktop_pending: 0});
    if (url.pathname === '/api/sessions') {
      const q = url.searchParams.get('q') || '';
      const items = sessions.filter(s => (!url.searchParams.get('project') || s.project === url.searchParams.get('project')) && (!q || (s.title + conversations.get(s.id).map(m => m.body).join('\n')).includes(q))).map(s => {
        if (!q) return s;
        const matching = conversations.get(s.id).filter(m => m.body.includes(q));
        return {...s, matches: matching.length, match_id: matching[0]?.id, match_preview: matching[0]?.body || s.preview};
      });
      return send({items, more: false, offset: items.length, elapsed_ms: 3});
    }
    const match = decodeURIComponent(url.pathname).match(/^\/api\/sessions\/([^/]+)(\/search)?$/);
    if (match && conversations.has(match[1])) {
      const session = sessions.find(s => s.id === match[1]);
      const items = conversations.get(session.id);
      if (match[2]) return send({items: items.filter(m => m.body.includes(url.searchParams.get('q') || '')).map(m => ({id: m.id, preview: m.body})), more: false});
      return send({session, items, total: items.length, offset: 0, more: false});
    }
    if (url.pathname === '/api/sources') return send({sources: [
      {path: 'C:/Users/demo/.codex', platform: 'Windows', exists: true, kind: 'Codex'},
      {path: '/home/demo/.codex', platform: 'WSL', exists: true, kind: 'Codex'},
      {path: 'C:/Users/demo/AppData/Roaming/Codex/web/Codex/Default/Cache/Cache_Data', platform: 'Windows', exists: true, kind: 'ChatGPT Desktop'},
    ], chatgpt: '', desktop_capture: {enabled: true, supported: true, port: 19222}});
    if (url.pathname === '/api/backup/status') return send({directory: '/backups/gpt-vault', job: showBackup ? {id: 'demo-backup', mode: 'local', stage: 'ready', started: timestamp - 30, duration: 3, done_bytes: stats.db_bytes, total_bytes: stats.db_bytes, path: '/backups/gpt-vault/demo-archive.sqlite.zst', format: 'zstd', input_bytes: stats.db_bytes, output_bytes: 14 * 1024 ** 2} : null});
    if (url.pathname === '/api/restore/status') return send({job: null});
    if (url.pathname === '/' || url.pathname.startsWith('/assets/')) {
      const relative = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      const asset = resolve(root, 'dist', relative);
      if (!asset.startsWith(join(root, 'dist') + '/')) throw Error('Invalid asset path');
      return route.fulfill({body: await readFile(asset), contentType: relative.endsWith('.js') ? 'application/javascript' : relative.endsWith('.css') ? 'text/css' : 'text/html'});
    }
    failures.push('Unmocked request: ' + url.pathname);
    return route.abort();
  });
  const page = await context.newPage();
  page.on('pageerror', error => failures.push(error.message));
  await page.clock.install({time});
  await page.goto(origin + '/?session=chatgpt%3Ademo-archive');
  await expect(page.locator('.reader-header h2')).toHaveText('把对话整理成个人知识库');
  await expect(page.locator('#messages table')).toBeVisible();
  await expect(page.locator('.conversation-card')).toHaveCount(sessions.length);
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({path: join(output, 'conversation.png'), animations: 'disabled'});

  await page.locator('#theme').click();
  await page.locator('#global-search').fill('搜索');
  await expect(page.locator('#search-summary')).toContainText('包含「搜索」');
  await page.locator('[data-id="demo-search"]').click();
  await expect(page.locator('.reader-header h2')).toHaveText('给笔记应用加入全文搜索');
  await expect(page.locator('#messages pre')).toBeVisible();
  // Show the start of the answer rather than auto-scroll to a lower search hit.
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await page.locator('#message-scroll').evaluate(el => el.scrollTop = 0);
  await page.screenshot({path: join(output, 'search-dark.png'), animations: 'disabled'});

  await page.locator('#theme').click();
  await page.locator('#clear-search').click();
  showBackup = true;
  await page.locator('#sources-button').click();
  await expect(page.locator('#sources-dialog')).toBeVisible();
  await expect(page.locator('#backup-progress')).toContainText('demo-archive.sqlite.zst');
  await page.clock.runFor(4500);
  await expect(page.locator('#toast')).not.toHaveClass(/visible/);
  await page.screenshot({path: join(output, 'backup.png'), animations: 'disabled'});
  if (failures.length) throw Error(failures.join('\n'));
  console.log('Saved three README screenshots using fictional data only: docs/images/');
} finally {
  await browser.close();
}
