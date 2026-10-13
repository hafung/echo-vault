import './style.css';
import { createElement, Search, Folder, MessagesSquare, Star, RefreshCw, ArrowUpRight, ChevronDown, ChevronLeft, ChevronRight, X, Check, Download, Copy, SlidersHorizontal, Database, ShieldCheck, Terminal, PanelLeft, Moon, Sun, ArrowDown, ArrowUp, ArrowDownToLine, ArrowUpToLine, MoreHorizontal, FileText, Upload, CircleHelp, Monitor, Command, BookOpen, LoaderCircle, TextSearch, Layers, CheckCircle2, EyeOff, Eye, Trash2 } from 'lucide';
import { marked } from 'marked';
import { localPathTarget, barePathPattern } from './local-paths';
import { renderMessageContent, decorateMessageImages, type ChatGPTUI } from './message-content';

type Node = Parameters<typeof createElement>[0];
const icons: Record<string, Node> = { Search, Folder, MessagesSquare, Star, RefreshCw, ArrowUpRight, ChevronDown, ChevronLeft, ChevronRight, X, Check, Download, Copy, SlidersHorizontal, Database, ShieldCheck, Terminal, PanelLeft, Moon, Sun, ArrowDown, ArrowUp, ArrowDownToLine, ArrowUpToLine, MoreHorizontal, FileText, Upload, CircleHelp, Monitor, Command, BookOpen, LoaderCircle, TextSearch, Layers, CheckCircle2, EyeOff, Eye, Trash2 };
function icon(name: string, cls = '') { const svg = createElement(icons[name] || FileText); svg.setAttribute('class', 'icon ' + cls); svg.setAttribute('aria-hidden', 'true'); return svg.outerHTML; }
function esc(value: unknown) { return String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!); }
function $(selector: string, root: ParentNode = document) { return root.querySelector<HTMLElement>(selector)!; }
function input(selector: string) { return document.querySelector<HTMLInputElement>(selector)!; }
function num(value: number) { return new Intl.NumberFormat('zh-CN').format(value || 0); }
function size(value: number) { return value < 1024 ** 3 ? (value / 1024 ** 2).toFixed(1) + ' MB' : (value / 1024 ** 3).toFixed(2) + ' GB'; }
function date(value: number, full = false) { if (!value) return '尚未同步'; return new Date(value * 1000).toLocaleString('zh-CN', full ? { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false } : { month: '2-digit', day: '2-digit' }); }
function relative(value: number) { if (!value) return '尚未同步'; const diff = Date.now() / 1000 - value; return diff < 60 ? '刚刚' : diff < 3600 ? `${Math.floor(diff / 60)} 分钟前` : diff < 86400 ? `${Math.floor(diff / 3600)} 小时前` : date(value); }
function highlight(text: string, query = state.q) {
  let result = esc(text);
  const terms = (query.match(/"[^"]+"|\S+/g) || []).map(t => esc(t.replace(/^"|"$/g, ''))).sort((a, b) => b.length - a.length);
  if (terms.length) result = result.replace(new RegExp(terms.map(t => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|'), 'gi'), '<mark>$&</mark>');
  return result;
}

interface Session { id: string; title: string; project: string; cwd: string; provider: string; platform: string; model: string; created: number; updated: number; message_count: number; total_count: number; favorite: number; archived: number; hidden: number; project_hidden: number; parent_id: string | null; preview: string; match_id: number | null; matches: number; match_preview: string; sources?: { path: string; missing: number; last_sync: number }[] }
interface Message { id: number; role: string; kind: string; phase: string; timestamp: number; body: string; truncated: boolean; length: number; metadata: { branch?: boolean; assets?: (string | { reference: string })[]; chatgpt_ui?: ChatGPTUI } }
interface Page { session: Session; items: Message[]; offset: number; total: number; more: boolean }
interface Stats { sessions: number; messages: number; events: number; projects: number; favorites: number; projects_list: { project: string; count: number; updated: number; hidden: number }[]; platforms: { platform: string; count: number }[]; providers: { provider: string; count: number }[]; last_sync: number; db_bytes: number; raw_bytes: number }
interface Sync { running: boolean; automatic?: boolean; stage: string; done: number; total: number; imported: number; skipped: number; errors: { path: string; error: string }[]; warnings: { path: string; error: string }[]; duration?: number; started?: number; finished?: number; file: string }
interface BackupJob { id: string; mode: 'local' | 'download'; stage: 'copying' | 'compressing' | 'finishing' | 'ready' | 'error' | 'expired'; started: number; duration: number; done_bytes: number; total_bytes: number; path?: string; error?: string; format?: string; input_bytes?: number; output_bytes?: number }
interface BackupStatus { directory: string; job: BackupJob | null }
let backupState: BackupStatus | null = null, backupStarting = false, backupPolling = false;
let backupTimer = 0;
interface Source { path: string; platform: string; exists: boolean; kind: string }
const url = new URL(location.href);
const state = { q: url.searchParams.get('q') || '', project: url.searchParams.get('project') || '', favorite: url.searchParams.get('favorite') === '1', platform: url.searchParams.get('platform') || '', provider: '', sort: 'recent', from: '', to: '', all: false, showHidden: url.searchParams.get('hidden') === '1', id: url.searchParams.get('session') || '', items: [] as Session[], more: false, offset: 0, loading: false, session: null as Session | null, page: null as Page | null, stats: null as Stats | null, sync: null as Sync | null, localSearchOpen: false, localQuery: '', hits: [] as { id: number; role: string; preview: string }[], hitIndex: 0 };
let listAbort: AbortController | null = null, readerAbort: AbortController | null = null, hitAbort: AbortController | null = null;
let listVersion = 0, readerVersion = 0, statsVersion = 0, syncBusy = false;
let syncStarting = false, syncTimer = 0;
let lastAutoSyncAttempt = 0;
const automaticSyncInterval = 5 * 60 * 1000;
const readerCache = new Map<string, Page>();
const readSize = Number(localStorage.getItem('echo-font')) || 15;
document.documentElement.style.setProperty('--read-size', readSize + 'px');
document.documentElement.dataset.theme = localStorage.getItem('echo-theme') || 'light';
marked.setOptions({ breaks: true, gfm: true });
function localPathHTML(path: string, label: string) { return `<button type="button" class="local-path-link" data-local-path="${esc(path)}" title="打开本地文件或文件夹 · ${esc(path)}">${label}</button>`; }
marked.use({ renderer: { link({ href, title, tokens }) {
  const path = localPathTarget(href, state.session?.cwd, true);
  const label = this.parser.parseInline(tokens);
  if (path) return localPathHTML(path, label);
  return `<a href="${esc(href)}"${title ? ` title="${esc(title)}"` : ''}>${label}</a>`;
} } });

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch('/api' + path, options);
  if (!res.ok) { let reason = `请求失败 (${res.status})`; try { reason = (await res.json()).error || reason; } catch { /* text error */ } throw new Error(reason); }
  return res.json() as Promise<T>;
}
function post<T>(path: string, value = {}) { return api<T>(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Echo-Request': '1' }, body: JSON.stringify(value) }); }
function toast(text: string, error = false) { const el = $('#toast'); el.innerHTML = icon(error ? 'CircleHelp' : 'CheckCircle2') + esc(text); el.className = 'toast visible' + (error ? ' error' : ''); window.clearTimeout(toastTimer); toastTimer = window.setTimeout(() => el.classList.remove('visible'), 4200); }
let toastTimer = 0;
function handle(error: unknown) { if (error instanceof DOMException && error.name === 'AbortError') return; toast(error instanceof Error ? error.message : String(error), true); }
function saveUrl(push = false) { const next = new URL(location.href); for (const [key, value] of Object.entries({ q: state.q, project: state.project, favorite: state.favorite ? '1' : '', platform: state.platform, hidden: state.showHidden ? '1' : '', session: state.id })) value ? next.searchParams.set(key, value) : next.searchParams.delete(key); if (push) history.pushState({}, '', next); else history.replaceState({}, '', next); }

$('#app').innerHTML = `
  <div class="sidebar-shell"><aside id="archive-sidebar" class="sidebar" aria-label="档案导航">
    <a class="brand" href="/" aria-label="回声首页"><span class="brand-mark"><i></i><i></i><i></i></span><span>回声<small>ECHO VAULT</small></span></a>
    <div class="nav-label">你的对话档案</div>
    <nav class="main-nav"><button data-nav="all" class="nav-item active">${icon('MessagesSquare')}<span>全部对话</span><b id="all-count">—</b></button><button data-nav="favorite" class="nav-item">${icon('Star')}<span>我的收藏</span><b id="favorite-count">0</b></button></nav>
    <div class="project-heading"><div class="project-heading-label"><span class="nav-label">项目空间</span><button id="show-hidden" class="icon-button visibility-toggle" type="button" aria-label="显示隐藏 / 归档" aria-pressed="${state.showHidden}" title="显示隐藏 / 归档">${icon(state.showHidden ? 'Eye' : 'EyeOff')}</button></div><span id="project-count">—</span></div>
    <div class="project-search">${icon('Search')}<input id="project-search" aria-label="筛选项目" placeholder="查找项目"></div>
    <nav id="projects" class="projects" aria-label="项目"></nav>
    <div class="sidebar-bottom"><div class="local-note">${icon('ShieldCheck')}<div>只属于你的记忆<small>本地存储 · 原始记录只读</small></div></div><button id="sources-button" class="nav-item">${icon('Database')}<span>数据与来源</span>${icon('ArrowUpRight')}</button><div class="sidebar-footer"><span>个人档案室 <i></i> LOCAL</span><button id="theme" class="icon-button" title="切换深浅主题" aria-label="切换深浅主题">${icon('Moon')}</button></div></div>
  </aside><button id="sidebar-toggle" class="sidebar-toggle" aria-controls="archive-sidebar" aria-expanded="true" aria-label="收起项目导航" title="收起项目导航">${icon('ChevronLeft')}</button></div>
  <div class="sidebar-scrim"></div>
  <main class="workspace">
    <header class="topbar"><div class="breadcrumb"><button id="mobile-menu" class="icon-button" aria-label="打开导航">${icon('PanelLeft')}</button><span>私人档案室</span><span class="slash">/</span><strong id="crumb">全部对话</strong></div><div class="top-actions"><span class="connection"><i></i> 本地已连接</span><button id="sync-button" class="button primary">${icon('RefreshCw')}<span>同步对话</span></button></div></header>
    <div class="searchbar"><div class="global-search">${icon('Search')}<input id="global-search" type="search" autocomplete="off" aria-label="搜索所有项目的对话" placeholder="搜索每一次对话、每一个想法…" value="${esc(state.q)}"><button id="clear-search" class="icon-button" title="清除搜索" aria-label="清除搜索" hidden>${icon('X')}</button><kbd>Ctrl K</kbd></div><button id="filter-button" class="button filter-button" aria-expanded="false">${icon('SlidersHorizontal')}<span>筛选</span><i id="filter-dot" hidden></i></button></div>
    <div id="filters" class="filters" hidden><label>来源<select id="platform-filter"><option value="">全部设备</option><option>WSL</option><option>Windows</option><option>云端导出</option></select></label><label>应用<select id="provider-filter"><option value="">全部应用</option><option>Codex</option><option>ChatGPT</option></select></label><label>从<input id="date-from" type="date"></label><label>至<input id="date-to" type="date"></label><label class="checkbox"><input id="search-all" type="checkbox"> 搜索工具与上下文</label><button id="reset-filters" class="text-button">重置</button></div>
    <div class="panes"><section class="conversation-pane" aria-label="对话列表"><div class="list-heading"><div><span class="eyebrow">THE ARCHIVE</span><h1 id="list-title">全部对话 <span id="list-count"></span></h1></div><select id="sort" aria-label="对话排序"><option value="recent">最近更新</option><option value="oldest">最早更新</option></select></div><div id="search-summary" class="search-summary">那些值得留下的思考，都在这里。</div><div id="conversation-list" class="conversation-list" role="list"></div><div class="list-footer"><span id="list-footer-text">正在读取档案…</span><span>${icon('ShieldCheck')} 私密</span></div></section>
    <section class="reader" aria-label="对话阅读区"><div id="reader-content" class="reader-content"></div></section></div>
    <footer class="statusbar"><span id="sync-status"><i class="status-dot"></i> 准备就绪</span><span id="storage-status">本地档案</span></footer>
  </main>
  <dialog id="sources-dialog" class="sources-dialog"><div class="dialog-heading"><div><span class="eyebrow">YOUR PRIVATE COLLECTION</span><h2>数据与来源</h2></div><div id="sources-summary" class="storage-overview"></div><button class="icon-button" data-close-dialog aria-label="关闭">${icon('X')}</button></div><div id="sources-content"></div></dialog>
  <dialog id="info-dialog" class="info-dialog"><div class="dialog-heading"><h2>对话详情</h2><button class="icon-button" data-close-dialog aria-label="关闭">${icon('X')}</button></div><div id="info-content"></div></dialog>
  <dialog id="help-dialog" class="info-dialog"><div class="dialog-heading"><h2>让记忆触手可及</h2><button class="icon-button" data-close-dialog aria-label="关闭">${icon('X')}</button></div><div class="shortcut-list"><p><span>全局搜索</span><kbd>Ctrl / ⌘ K</kbd></p><p><span>当前对话搜索</span><kbd>Ctrl / ⌘ F</kbd></p><p><span>上下切换对话</span><kbd>↑ ↓</kbd></p><p><span>关闭面板 / 清除搜索</span><kbd>Esc</kbd></p><p class="muted">空格分隔多个关键词可交叉搜索；双引号表示完整短语。中文单字、双字同样可搜索。</p></div></dialog>
  <dialog id="local-path-dialog" class="info-dialog" aria-labelledby="local-path-title"><div class="dialog-heading"><h2 id="local-path-title">打开本地文件或文件夹</h2><button class="icon-button" data-close-dialog aria-label="关闭">${icon('X')}</button></div><p class="local-path-description">确认后将通过本机应用打开此路径。</p><code id="local-path-value"></code><p id="local-path-error" role="alert" hidden></p><div class="local-path-actions"><button id="local-path-cancel" class="button">取消</button><button id="local-path-reveal" class="button">${icon('Folder')}打开所在文件夹</button><button id="local-path-open" class="button primary">${icon('ArrowUpRight')}确认打开</button></div></dialog>
  <dialog id="delete-session-dialog" class="info-dialog" aria-labelledby="delete-session-title" aria-describedby="delete-session-description"><div class="dialog-heading"><h2 id="delete-session-title">删除对话？</h2><button class="icon-button" data-close-dialog aria-label="关闭">${icon('X')}</button></div><p id="delete-session-name" class="delete-session-name"></p><p id="delete-session-description" class="local-path-description">将从本地档案中删除这段对话及其记录，原始来源文件保持不变。删除后不会通过增量同步重新收录；「完整重新扫描」可从仍可读取的来源重新收录。</p><p id="delete-session-error" role="alert" hidden></p><div class="local-path-actions"><button id="delete-session-cancel" class="button" autofocus>取消</button><button id="delete-session-confirm" class="button danger">${icon('Trash2')}确认删除</button></div></dialog>
  <div id="backup-activity" class="backup-activity" hidden role="status" aria-live="polite"></div>
  <div id="toast" class="toast" role="status" aria-live="polite"></div>`;

function welcome() {
  const s = state.stats;
  $('#reader-content').innerHTML = `<div class="welcome"><div class="welcome-top"><span class="eyebrow">A SPACE FOR YOUR THOUGHTS</span><button id="help-button" class="icon-button" aria-label="键盘快捷键" title="键盘快捷键">${icon('CircleHelp')}</button></div><div class="echo-art" aria-hidden="true"><div class="orbit orbit-1"></div><div class="orbit orbit-2"></div><div class="orbit orbit-3"></div><div class="art-center"><span class="brand-mark"><i></i><i></i><i></i></span></div><span class="art-dot dot-1"></span><span class="art-dot dot-2"></span><span class="art-line line-1"></span><span class="art-line line-2"></span><span class="art-label label-1">THOUGHTS</span><span class="art-label label-2">CONNECTIONS</span></div><span class="welcome-kicker">留住思考的轨迹</span><h2>每一次对话，<br>都值得被<span>记住。</span></h2><p>从一个灵感，到一个完成的项目。<br>把散落各处的对话，收进同一处安静的空间。</p><div class="welcome-stats"><div><b>${num(s?.sessions || 0)}</b><span>段对话</span></div><div><b>${num(s?.projects || 0)}</b><span>个项目空间</span></div><div><b>${num(s?.messages || 0)}</b><span>条记忆</span></div></div><div class="welcome-hint">${icon('ChevronLeft')} 选择一段对话，继续那时的思考 <span>或按 <kbd>Ctrl K</kbd> 搜索</span></div><div class="welcome-bottom"><span><i></i> 全部保存在你的设备上</span><span>YOUR THOUGHTS, KEPT CLOSE.</span></div></div>`;
  $('#help-button').onclick = () => (document.querySelector('#help-dialog') as HTMLDialogElement).showModal();
}

function renderProjects() {
  const query = input('#project-search').value.toLowerCase();
  $('#projects').innerHTML = (state.stats?.projects_list || []).filter(p => p.project.toLowerCase().includes(query)).map((p, i) => `<div class="project-row ${p.hidden ? 'is-hidden' : ''}"><button class="project-item ${state.project === p.project ? 'active' : ''}" data-project="${esc(p.project)}" aria-pressed="${state.project === p.project}" title="${esc(p.project)} · 最近更新 ${date(p.updated, true)}"><span class="project-symbol tone-${i % 5}">${icon('Folder')}</span><span class="project-name">${esc(p.project)}</span><b>${num(p.count)}</b></button><button class="icon-button project-visibility" data-project-visibility="${esc(p.project)}" data-hidden="${p.hidden}" aria-label="${p.hidden ? '恢复项目' : '隐藏项目'} ${esc(p.project)}" title="${p.hidden ? '恢复项目' : '隐藏项目'}">${icon(p.hidden ? 'Eye' : 'EyeOff')}</button></div>`).join('') || '<p class="empty-projects">没有匹配的项目</p>';
  $('#projects').querySelectorAll<HTMLButtonElement>('[data-project]').forEach(el => el.onclick = () => { state.project = state.project === el.dataset.project ? '' : el.dataset.project!; state.favorite = false; updateNav(); saveUrl(); loadList(); closeSidebar(); });
  $('#projects').querySelectorAll<HTMLButtonElement>('[data-project-visibility]').forEach(el => el.onclick = () => setVisibility('project', el.dataset.projectVisibility!, el.dataset.hidden !== '1'));
}
function clearReader() {
  hitAbort?.abort(); state.localQuery = ''; state.hits = []; state.hitIndex = 0;
  readerAbort?.abort(); readerVersion++;
  state.id = ''; state.session = null; state.page = null; state.localSearchOpen = false;
  document.body.classList.remove('reading'); welcome();
}
async function setVisibility(kind: 'project' | 'session', value: string, hidden: boolean) {
  try {
    await post(kind === 'project' ? '/projects/visibility' : '/sessions/'+encodeURIComponent(value)+'/visibility', kind === 'project' ? {project: value, hidden} : {hidden});
    readerCache.clear();
    if (hidden && !state.showHidden) {
      if (kind === 'project' && state.project === value) state.project = '';
      if ((kind === 'project' && state.session?.project === value) || (kind === 'session' && state.id === value)) clearReader();
    } else if (state.session && ((kind === 'project' && state.session.project === value) || (kind === 'session' && state.id === value))) {
      if (kind === 'session') state.session.hidden = Number(hidden); else state.session.project_hidden = Number(hidden);
      if (state.page) renderReader(state.page);
    }
    await refreshStats(); updateNav(); saveUrl(); await loadList();
    toast(hidden ? '已隐藏，同步后仍保持隐藏' : '已恢复显示');
  } catch (error) { handle(error); }
}
let deletingSession: Session | null = null, deleteBusy = false;
function confirmSessionDelete() {
  if (!state.session || deleteBusy) return;
  deletingSession = state.session;
  $('#delete-session-name').textContent = deletingSession.title;
  $('#delete-session-error').hidden = true;
  (document.querySelector('#delete-session-dialog') as HTMLDialogElement).showModal();
}
async function deleteSession() {
  if (!deletingSession || deleteBusy) return;
  const session = deletingSession;
  const dialog = document.querySelector('#delete-session-dialog') as HTMLDialogElement;
  const button = $('#delete-session-confirm') as HTMLButtonElement;
  deleteBusy = true;
  dialog.querySelectorAll<HTMLButtonElement>('button').forEach(control => control.disabled = true);
  button.setAttribute('aria-busy', 'true');
  button.innerHTML = `${icon('LoaderCircle', 'spinning')}正在删除…`;
  $('#delete-session-error').hidden = true;
  try {
    await post('/sessions/'+encodeURIComponent(session.id)+'/delete', {confirmed: true});
    readerCache.clear();
    listAbort?.abort(); listVersion++;
    state.items = state.items.filter(s => s.id !== session.id);
    if (state.id === session.id) clearReader();
    saveUrl(); renderList();
    dialog.close(); deletingSession = null;
    toast('对话已删除，增量同步不会重新收录');
    await refreshStats();
    if (state.project && !state.stats?.projects_list.some(p => p.project === state.project)) state.project = '';
    updateNav(); saveUrl(); await loadList();
  } catch (error) {
    if (dialog.open) {
      $('#delete-session-error').textContent = error instanceof Error ? error.message : String(error);
      $('#delete-session-error').hidden = false;
    } else handle(error);
  } finally {
    deleteBusy = false;
    dialog.querySelectorAll<HTMLButtonElement>('button').forEach(control => control.disabled = false);
    button.removeAttribute('aria-busy');
    button.innerHTML = `${icon('Trash2')}确认删除`;
  }
}
$('#delete-session-cancel').onclick = () => (document.querySelector('#delete-session-dialog') as HTMLDialogElement).close();
$('#delete-session-confirm').onclick = deleteSession;
$('#delete-session-dialog').addEventListener('cancel', event => { if (deleteBusy) event.preventDefault(); });
function renderVisibilityToggle() {
  const button = $('#show-hidden');
  button.innerHTML = icon(state.showHidden ? 'Eye' : 'EyeOff');
  button.setAttribute('aria-pressed', String(state.showHidden));
  button.title = state.showHidden ? '正在显示隐藏 / 归档 · 点击恢复隐藏' : '显示隐藏 / 归档';
  button.setAttribute('aria-label', state.showHidden ? '不显示隐藏 / 归档' : '显示隐藏 / 归档');
}
async function changeVisibilityScope() {
  state.showHidden = !state.showHidden;
  renderVisibilityToggle();
  if (!state.showHidden && (state.session?.hidden || state.session?.project_hidden)) clearReader();
  await refreshStats();
  if (state.project && !state.stats?.projects_list.some(p => p.project === state.project)) state.project = '';
  updateNav(); saveUrl(); await loadList();
  if (state.id && !state.session) await openSession(state.id);
}
function updateNav() {
  const label = state.project || (state.favorite ? '我的收藏' : '全部对话');
  $('#crumb').textContent = label;
  input('#global-search').placeholder = state.project ? `搜索「${state.project}」中的对话…` : '搜索所有项目的对话…';
  input('#global-search').setAttribute('aria-label', state.project ? `搜索项目 ${state.project} 的对话` : '搜索所有项目的对话');
  $('#list-title').innerHTML = esc(label) + ' <span id="list-count"></span>';
  document.querySelectorAll<HTMLElement>('[data-nav]').forEach(el => el.classList.toggle('active', !state.project && (el.dataset.nav === 'favorite') === state.favorite));
  renderProjects();
  $('#list-count').textContent = state.q ? `${state.items.length}${state.more ? '+' : ''}` : num(state.project ? state.stats?.projects_list.find(p => p.project === state.project)?.count || 0 : state.favorite ? state.stats?.favorites || 0 : state.stats?.sessions || 0);
}
async function refreshStats() {
  const version = ++statsVersion;
  const stats = await api<Stats>('/stats?hidden='+(state.showHidden ? '1' : '0'));
  if (version !== statsVersion) return;
  state.stats = stats; const s = stats;
  $('#all-count').textContent = num(s.sessions); $('#favorite-count').textContent = num(s.favorites); $('#project-count').textContent = num(s.projects);
  $('#storage-status').textContent = `${num(s.messages)} 条消息 · ${size(s.db_bytes)} · 上次同步 ${relative(s.last_sync)}`;
  renderProjects(); if (!state.session) welcome();
}
function listParams(offset = 0) { return new URLSearchParams({ q: state.q, project: state.project, favorite: state.favorite ? '1' : '', platform: state.platform, provider: state.provider, sort: state.sort, from: state.from, to: state.to, all: state.all ? '1' : '', hidden: state.showHidden ? '1' : '', offset: String(offset) }).toString(); }
function skeleton() { return Array.from({ length: 5 }, () => '<div class="skeleton-card"><i></i><i></i><i></i></div>').join(''); }
function rowHTML(s: Session) { return `<button class="conversation-card ${s.id === state.id ? 'selected' : ''}" data-id="${esc(s.id)}" role="listitem"><div class="card-top"><span class="project-tag">${icon(s.provider === 'ChatGPT' ? 'MessagesSquare' : 'Terminal')}${esc(s.project)}</span><time title="${date(s.updated, true)}">${date(s.updated)}</time></div><h3>${highlight(s.title)}</h3><p>${highlight(s.match_preview || s.preview || '打开这段对话，看看当时的思考。')}</p><div class="card-bottom"><span>${s.matches ? `${icon('TextSearch')} ${num(s.matches)} 处匹配` : `${icon('MessagesSquare')} ${num(s.message_count)} 条消息`}<i>·</i>${esc(s.platform)}</span><span class="card-badges">${s.parent_id ? '<span title="子代理会话">'+icon('Layers')+'</span>' : ''}${s.hidden || s.project_hidden ? '<span title="已隐藏 / 归档">'+icon('EyeOff')+'</span>' : ''}${s.favorite ? icon('Star', 'filled') : ''}${icon('ArrowUpRight','card-arrow')}</span></div></button>`; }
function renderList(append = false, appended = 35) {
  const list = $('#conversation-list');
  if (!append) list.innerHTML = state.items.map(rowHTML).join('') || `<div class="empty-list">${icon(state.q ? 'Search' : 'MessagesSquare')}<h3>${state.q ? '没有找到这段记忆' : state.favorite ? '收藏值得重访的对话' : '这里还没有对话'}</h3><p>${state.q ? '换一个关键词，或放宽筛选条件。' : state.favorite ? '打开对话，点击右上角的星标即可收藏。' : '点击「同步对话」收集本机记录，<br>或在数据与来源中导入 ChatGPT。'}</p>${state.q ? '<button class="text-button" id="empty-reset">清除搜索与筛选</button>' : ''}</div>`;
  else { $('#load-more')?.remove(); const fragment = document.createElement('div'); fragment.innerHTML = state.items.slice(-appended).map(rowHTML).join(''); while (fragment.firstChild) list.append(fragment.firstChild); }
  $('#load-more')?.remove();
  if (state.more) list.insertAdjacentHTML('beforeend', `<button id="load-more" class="load-more">${icon('ChevronDown')} 加载更多对话</button>`);
  list.querySelectorAll<HTMLButtonElement>('[data-id]').forEach(el => el.onclick = () => { const s = state.items.find(s => s.id === el.dataset.id)!; openSession(s.id, s.match_id || undefined, true); });
  if ($('#load-more')) $('#load-more').onclick = () => loadList(true);
  if ($('#empty-reset')) $('#empty-reset').onclick = () => { state.q = ''; state.project = ''; state.favorite = false; input('#global-search').value = ''; resetFilters(); updateNav(); saveUrl(); loadList(); };
  $('#list-count').textContent = state.q ? `${state.items.length}${state.more ? '+' : ''}` : num(state.project ? state.stats?.projects_list.find(p => p.project === state.project)?.count || 0 : state.favorite ? state.stats?.favorites || 0 : state.stats?.sessions || 0);
  $('#list-footer-text').textContent = state.items.length ? `已展示 ${num(state.items.length)} 段对话${state.more ? '' : ' · 已到底'} ` : '同步或导入对话，开始收录';
}
async function loadList(append = false) {
  if (append && state.loading) return;
  const version = ++listVersion; listAbort?.abort(); listAbort = new AbortController(); state.loading = true;
  $('#clear-search').hidden = !state.q;
  if (!append) { state.offset = 0; state.items = []; $('#conversation-list').innerHTML = skeleton(); }
  try {
    const result = await api<{ items: Session[]; more: boolean; offset: number; elapsed_ms: number }>('/sessions?' + listParams(append ? state.offset : 0), { signal: listAbort.signal });
    if (version !== listVersion) return;
    state.items = append ? [...state.items, ...result.items] : result.items; state.more = result.more; state.offset = result.offset;
    $('#search-summary').innerHTML = state.q ? `<span>包含「${esc(state.q)}」的对话</span><span>${result.elapsed_ms} ms</span>` : `<span>那些值得留下的思考，都在这里。</span>`;
    renderList(append,result.items.length); if (!append) $('#conversation-list').scrollTop = 0;
  } catch (error) { if (version === listVersion && !(error instanceof DOMException && error.name === 'AbortError')) { $('#conversation-list').innerHTML = '<div class="empty-list"><h3>读取暂时失败</h3><p>请确认本地服务正在运行。</p><button id="retry-list" class="button">重新加载</button></div>'; $('#retry-list').onclick = () => loadList(); } handle(error); }
  finally { if (version === listVersion) state.loading = false; }
}

function renderMessage(m: Message) {
  const technical = !['user', 'assistant'].includes(m.role);
  const labels: Record<string, string> = { user: '你', assistant: state.session?.provider === 'ChatGPT' ? 'GPT' : 'Codex', tool: '工具调用', reasoning: '思考过程', context: '上下文' };
  const rendered = renderMessageContent(m.body, m.metadata.chatgpt_ui, state.session?.provider === 'ChatGPT');
  const body = `<div class="prose" data-body="${m.id}">${rendered}</div>${m.metadata.assets?.map(a => typeof a === 'string' ? `<a class="image-link" href="/api/assets/${esc(a)}" target="_blank" rel="noopener"><img src="/api/assets/${esc(a)}" loading="lazy" alt="对话附件"></a>` : `<span class="attachment-reference">${icon('FileText')}附件引用 · ${esc(a.reference)}<small>原附件未在本机找到，引用已保留</small></span>`).join('') || ''}${m.truncated ? `<button class="text-button expand-message" data-mid="${m.id}">展开完整内容（${num(m.length)} 字） ${icon('ChevronDown')}</button>` : ''}`;
  return `<article class="message ${esc(m.role)} ${state.hits[state.hitIndex]?.id === m.id ? 'hit-message' : ''}" id="message-${m.id}" data-mid="${m.id}"><div class="message-avatar">${m.role === 'user' ? 'Z' : icon(technical ? m.role === 'reasoning' ? 'Layers' : 'Terminal' : 'Command')}</div><div class="message-main"><div class="message-heading"><b>${labels[m.role] || m.role}</b>${m.phase === 'commentary' ? '<span class="phase-tag">进展</span>' : ''}${m.metadata.branch ? '<span class="phase-tag">其他分支</span>' : ''}<time>${date(m.timestamp,true)}</time><button class="icon-button copy-message" data-mid="${m.id}" title="复制消息" aria-label="复制消息">${icon('Copy')}</button></div>${technical ? `<details class="technical"><summary>${esc(m.kind)} <span>${esc(m.body.slice(0,90).replace(/\s+/g,' '))}</span>${icon('ChevronDown')}</summary>${body}</details>` : body}</div></article>`;
}
function showLocalPath(path: string) {
  const dialog = document.querySelector<HTMLDialogElement>('#local-path-dialog')!;
  $('#local-path-value').textContent = path;
  $('#local-path-error').hidden = true;
  $('#local-path-cancel').onclick = () => dialog.close();
  const open = async (action: 'open' | 'reveal') => {
    const buttons = dialog.querySelectorAll<HTMLButtonElement>('.local-path-actions button');
    buttons.forEach(button => button.disabled = true);
    $('#local-path-error').hidden = true;
    try {
      await post('/local/open', { path, action, confirmed: true });
      dialog.close(); toast(action === 'reveal' ? '已请求打开所在文件夹' : '已请求通过本机应用打开');
    } catch (error) {
      $('#local-path-error').textContent = error instanceof Error ? error.message : String(error);
      $('#local-path-error').hidden = false;
    } finally { buttons.forEach(button => button.disabled = false); }
  };
  $('#local-path-open').onclick = () => open('open');
  $('#local-path-reveal').onclick = () => open('reveal');
  dialog.showModal();
}
function decorateProse() {
  const root = $('#messages');
  root.querySelectorAll<HTMLAnchorElement>('.prose a').forEach(a => {
    const path = localPathTarget(a.getAttribute('href') || '', state.session?.cwd, true);
    if (path) { a.outerHTML = localPathHTML(path, a.innerHTML); return; }
    a.target = '_blank'; a.rel = 'noopener noreferrer';
  });
  root.querySelectorAll<HTMLElement>('.prose code:not(pre code)').forEach(code => {
    if (code.closest('a, button')) return;
    const path = localPathTarget(code.textContent || '', state.session?.cwd);
    if (path) code.innerHTML = localPathHTML(path, esc(code.textContent));
  });
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT); const nodes: Text[] = [];
  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    if (node.parentElement?.closest('.prose') && !node.parentElement.closest('a, button, code, pre')) nodes.push(node);
  }
  for (const node of nodes) {
    const text = node.textContent || ''; const fragment = document.createDocumentFragment(); let last = 0;
    for (const match of text.matchAll(barePathPattern)) {
      if (match.index && /[\w/\\:]/.test(text[match.index - 1])) continue;
      const label = match[0].replace(/[),.;!\]}）]+$/, '');
      const path = localPathTarget(label, state.session?.cwd);
      if (!path) continue;
      fragment.append(document.createTextNode(text.slice(last, match.index)));
      const button = document.createElement('button'); button.type = 'button'; button.className = 'local-path-link'; button.dataset.localPath = path; button.title = '打开本地文件或文件夹 · ' + path; button.textContent = label;
      fragment.append(button); last = match.index! + label.length;
    }
    if (last) { fragment.append(document.createTextNode(text.slice(last))); node.replaceWith(fragment); }
  }
  root.querySelectorAll<HTMLButtonElement>('.local-path-link').forEach(button => button.onclick = () => showLocalPath(button.dataset.localPath!));
  root.querySelectorAll<HTMLElement>('.prose pre').forEach(pre => { if (pre.querySelector('.code-copy')) return; const button = document.createElement('button'); button.className = 'code-copy'; button.title = '复制代码'; button.setAttribute('aria-label', '复制代码'); button.innerHTML = icon('Copy'); button.onclick = () => navigator.clipboard.writeText(pre.querySelector('code')?.textContent || '').then(() => toast('代码已复制')).catch(handle); pre.prepend(button); });
  root.querySelectorAll<HTMLButtonElement>('.copy-message').forEach(b => b.onclick = async () => { try { const data = await api<{ body: string }>('/messages/' + b.dataset.mid); await navigator.clipboard.writeText(data.body); toast('消息已复制'); } catch (error) { handle(error); } });
  root.querySelectorAll<HTMLButtonElement>('.expand-message').forEach(b => b.onclick = async () => { try { b.disabled = true; const data = await api<{ body: string }>('/messages/' + b.dataset.mid); const prose = $(`[data-body="${b.dataset.mid}"]`,root); const message = state.page?.items.find(item => String(item.id) === b.dataset.mid); prose.innerHTML = renderMessageContent(data.body, message?.metadata.chatgpt_ui, state.session?.provider === 'ChatGPT'); b.remove(); decorateProse(); markReader(); } catch (error) { b.disabled = false; handle(error); } });
  decorateMessageImages(root);
  markReader();
}
function markReader() {
  const root = $('#messages'); if (!root) return;
  root.querySelectorAll('mark').forEach(mark => mark.replaceWith(document.createTextNode(mark.textContent || '')));
  const terms = (state.localQuery || state.q).match(/"[^"]+"|\S+/g)?.map(t => t.replace(/^"|"$/g,'')) || [];
  if (!terms.length) return;
  const pattern = new RegExp(terms.map(t => t.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|'),'gi');
  const walker = document.createTreeWalker(root,NodeFilter.SHOW_TEXT); const nodes: Text[] = [];
  while(walker.nextNode()) { const node = walker.currentNode as Text; if (node.parentElement?.closest('.prose') && !node.parentElement.closest('button:not(.local-path-link)')) nodes.push(node); }
  let count = 0;
  for (const node of nodes) { pattern.lastIndex = 0; const text = node.textContent || ''; if (!pattern.test(text)) continue; pattern.lastIndex = 0; const fragment = document.createDocumentFragment(); let last = 0; for (const match of text.matchAll(pattern)) { if (++count > 1000) break; fragment.append(document.createTextNode(text.slice(last,match.index))); const mark = document.createElement('mark'); mark.textContent = match[0]; fragment.append(mark); last = match.index!+match[0].length; } fragment.append(document.createTextNode(text.slice(last))); node.replaceWith(fragment); if (count > 1000) break; }
}
function renderReader(page: Page, around?: number) {
  state.session = page.session; state.page = page; const s = page.session;
  document.body.classList.add('reading');
  const offset = page.offset, end = offset + page.items.length;
  $('#reader-content').innerHTML = `<header class="reader-header"><div class="reader-heading-top"><div class="reader-breadcrumb"><button id="back-list" class="icon-button" aria-label="返回对话列表">${icon('ChevronLeft')}</button><span>${icon('Folder')}${esc(s.project)}</span><i>/</i><span>${esc(s.provider)}<em>${esc(s.platform)}</em></span></div><div class="reader-actions"><button id="session-visibility" class="icon-button" title="${s.hidden ? '恢复对话' : '隐藏对话'}" aria-label="${s.hidden ? '恢复对话' : '隐藏对话'}">${icon(s.hidden ? 'Eye' : 'EyeOff')}</button><button id="session-delete" class="icon-button delete-session-button" title="删除对话" aria-label="删除对话">${icon('Trash2')}</button><button id="session-favorite" class="icon-button ${s.favorite ? 'is-favorite' : ''}" title="${s.favorite ? '取消收藏' : '收藏对话'}" aria-label="${s.favorite ? '取消收藏' : '收藏对话'}">${icon('Star',s.favorite ? 'filled' : '')}</button><button id="session-search" class="icon-button" title="搜索当前对话" aria-label="搜索当前对话" aria-controls="local-search" aria-expanded="${state.localSearchOpen}">${icon('Search')}</button><details class="export-menu"><summary class="icon-button" title="导出对话" aria-label="导出对话">${icon('Download')}</summary><div class="export-popover"><a href="/api/sessions/${encodeURIComponent(s.id)}/export?format=md" download>${icon('FileText')}导出 Markdown</a><a href="/api/sessions/${encodeURIComponent(s.id)}/export?format=json" download>${icon('Database')}导出 JSON</a><a href="/api/sessions/${encodeURIComponent(s.id)}/export?format=raw" download>${icon('Terminal')}导出原始事件</a></div></details><button id="session-info" class="icon-button" title="对话详情" aria-label="对话详情">${icon('MoreHorizontal')}</button></div></div><h2>${esc(s.title)}</h2><div class="reader-meta"><span>${icon('MessagesSquare')}${num(s.message_count)} 条消息</span><i>·</i><span>${date(s.created,true)}</span>${s.model ? `<i>·</i><span class="model-name">${esc(s.model)}</span>` : ''}${s.hidden ? '<span class="phase-tag">已隐藏 / 归档</span>' : ''}${s.project_hidden ? '<span class="phase-tag">项目已隐藏</span>' : ''}${s.parent_id ? '<span class="phase-tag">子代理会话</span>' : ''}</div></header>
  <div id="local-search" class="local-search" ${state.localSearchOpen ? '' : 'hidden'}>${icon('TextSearch')}<input id="local-query" aria-label="搜索当前对话" placeholder="在这段对话中查找…" value="${esc(state.localQuery)}"><span id="hit-count"></span><button id="prev-hit" class="icon-button" title="上一个匹配" aria-label="上一个匹配">${icon('ChevronLeft')}</button><button id="next-hit" class="icon-button" title="下一个匹配" aria-label="下一个匹配">${icon('ChevronRight')}</button><button id="close-local-search" class="icon-button" aria-label="关闭对话搜索">${icon('X')}</button></div>
  <div class="reading-toolbar"><label class="checkbox"><input id="show-all" type="checkbox" ${state.all ? 'checked' : ''}>${icon('Layers')} 展示工具与上下文</label><div><button id="font-smaller" class="text-size" title="缩小字号" aria-label="缩小字号">A−</button><button id="font-larger" class="text-size" title="放大字号" aria-label="放大字号">A+</button></div></div>
  <div class="reader-body"><div id="message-scroll" class="message-scroll"><div id="messages" class="messages">${offset ? `<button id="previous-messages" class="message-page-button">${icon('ArrowUp')} 查看前面的消息 <span>还有 ${num(offset)} 条</span></button>` : '<div class="conversation-start"><span>对话开始</span></div>'}${page.items.map(renderMessage).join('') || '<div class="empty-list"><p>此会话尚无可展示的正文。可开启工具与上下文，或导出原始记录。</p></div>'}${page.more ? `<button id="next-messages" class="message-page-button">${icon('ArrowDown')} 继续阅读 <span>还有 ${num(page.total-end)} 条</span></button>` : '<div class="conversation-end"><span>这段记忆，已妥善保存。</span>'+icon('ShieldCheck')+'</div>'}</div></div><nav class="reader-navigation" aria-label="对话位置"><button id="go-start" class="reader-jump" title="回到开头" aria-label="回到开头" aria-controls="message-scroll">${icon('ArrowUpToLine')}</button><button id="go-end" class="reader-jump" title="前往末尾" aria-label="前往末尾" aria-controls="message-scroll">${icon('ArrowDownToLine')}</button></nav></div><div class="reader-footer"><span>${num(offset+1)} – ${num(end)} / ${num(page.total)} 条<span class="footer-faint"> · 按需加载</span></span><span>只读档案 ${icon('ShieldCheck')}</span></div>`;
  bindReader(); decorateProse();
  if (around) requestAnimationFrame(() => { const target = $('#message-' + around); if (target) { target.classList.add('hit-message'); target.scrollIntoView({ block: 'center', behavior: 'instant' }); target.querySelector('details')?.setAttribute('open',''); } });
  if (state.localQuery) updateHitCount();
}
async function openSession(id: string, around?: number, push = false, offset = 0, direction: 'replace' | 'next' | 'previous' | 'start' | 'end' = 'replace') {
  const version = ++readerVersion; readerAbort?.abort(); readerAbort = new AbortController();
  const changed = state.id !== id; const previousPage = state.page; const focused = document.activeElement?.id === 'local-query'; const selection = focused ? input('#local-query').selectionStart : null;
  const scroll = $('#message-scroll');
  const anchor = (direction === 'next' || direction === 'previous') && scroll ? Array.from(scroll.querySelectorAll<HTMLElement>('.message')).find(m => m.getBoundingClientRect().bottom > scroll.getBoundingClientRect().top + 20) : undefined;
  const anchorDelta = anchor && scroll ? anchor.getBoundingClientRect().top - scroll.getBoundingClientRect().top : 0;
  state.id = id; saveUrl(push);
  if (changed) { hitAbort?.abort(); state.localSearchOpen = false; state.localQuery = ''; state.hits = []; state.hitIndex = 0; }
  document.querySelectorAll<HTMLElement>('.conversation-card').forEach(el => el.classList.toggle('selected',el.dataset.id === id));
  if (changed || !state.session) $('#reader-content').innerHTML = `<div class="reader-loading">${icon('LoaderCircle','spinning')}<span>打开这段记忆…</span></div>`;
  else $('#reader-content').setAttribute('aria-busy','true');
  document.body.classList.add('reading');
  const params = new URLSearchParams({ all: state.all ? '1' : '', hidden: state.showHidden ? '1' : '', offset: String(offset), ...(around ? { around: String(around) } : {}) });
  const key = id + '?' + params;
  try {
    let page = readerCache.get(key) || await api<Page>('/sessions/' + encodeURIComponent(id) + '?' + params, { signal: readerAbort.signal });
    if (version !== readerVersion) return;
    readerCache.set(key,page); if(readerCache.size>12)readerCache.delete(readerCache.keys().next().value!);
    if (!changed && previousPage && direction === 'next' && page.offset === previousPage.offset + previousPage.items.length) {
      const merged = [...previousPage.items,...page.items]; const trim = Math.max(0,merged.length-90);
      page = {...page,items:merged.slice(trim),offset:previousPage.offset+trim};
    } else if (!changed && previousPage && direction === 'previous' && page.offset+page.items.length === previousPage.offset) {
      const merged = [...page.items,...previousPage.items]; const kept = merged.slice(0,90);
      page = {...page,items:kept,more:page.offset+kept.length<page.total};
    }
    renderReader(page,around); $('#reader-content').removeAttribute('aria-busy');
    if (direction === 'start' || direction === 'end') { const container = $('#message-scroll'); container.scrollTo({ top: direction === 'end' ? container.scrollHeight : 0, behavior: 'instant' }); }
    if (anchor) { const restored = $('#'+anchor.id); const container = $('#message-scroll'); if(restored)container.scrollTop += restored.getBoundingClientRect().top-container.getBoundingClientRect().top-anchorDelta; }
    if (focused && !$('#local-search').hidden) { input('#local-query').focus({preventScroll:true}); if(selection!==null)input('#local-query').setSelectionRange(selection,selection); }
  }
  catch(error) { if (version === readerVersion && !(error instanceof DOMException && error.name === 'AbortError')) $('#reader-content').innerHTML = '<div class="empty-list"><h3>暂时无法打开这段对话</h3><p>请稍后重新选择；若对话已隐藏 / 归档，请点击「项目空间」旁的眼睛图标显示隐藏 / 归档后重试。</p></div>'; handle(error); }
}
function bindReader() {
  $('#back-list').onclick = () => { document.body.classList.remove('reading'); state.id = ''; state.session = null; state.page = null; saveUrl(); welcome(); document.querySelectorAll('.conversation-card').forEach(el => el.classList.remove('selected')); };
  $('#session-favorite').onclick = () => favorite();
  $('#session-visibility').onclick = () => setVisibility('session', state.id, !state.session!.hidden);
  $('#session-delete').onclick = confirmSessionDelete;
  $('#session-search').onclick = () => state.localSearchOpen ? closeLocalSearch() : showLocalSearch();
  $('#close-local-search').onclick = closeLocalSearch;
  input('#local-query').addEventListener('input',debounce(() => { if (!state.localSearchOpen || !input('#local-query')) return; state.localQuery = input('#local-query').value.trim(); findHits(); },160));
  input('#local-query').onkeydown = e => { if(e.key==='Enter'){e.preventDefault();stepHit(e.shiftKey ? -1 : 1);} };
  $('#prev-hit').onclick = () => stepHit(-1); $('#next-hit').onclick = () => stepHit(1);
  $('#session-info').onclick = sessionInfo;
  input('#show-all').onchange = () => { state.all = input('#show-all').checked; input('#search-all').checked = state.all; readerCache.clear(); openSession(state.id); if(state.q)loadList(); };
  $('#font-smaller').onclick = () => adjustFont(-1); $('#font-larger').onclick = () => adjustFont(1);
  if ($('#previous-messages')) $('#previous-messages').onclick = () => openSession(state.id, undefined, false, Math.max(0,state.page!.offset-30),'previous');
  if ($('#next-messages')) $('#next-messages').onclick = () => openSession(state.id, undefined, false, state.page!.offset+state.page!.items.length,'next');
  $('#go-start').onclick = () => openSession(state.id,undefined,false,0,'start');
  $('#go-end').onclick = () => openSession(state.id,undefined,false,Math.max(0,state.page!.total-30),'end');
}
function adjustFont(delta: number) { const value = Math.max(12,Math.min(20,Number(localStorage.getItem('echo-font')||15)+delta)); localStorage.setItem('echo-font',String(value)); document.documentElement.style.setProperty('--read-size',value+'px'); }
let favoriteBusy = false;
async function favorite() { if(!state.session || favoriteBusy)return; favoriteBusy = true; const savingButton = $('#session-favorite') as HTMLButtonElement; savingButton.disabled = true; try { const value = !state.session.favorite; await post('/sessions/'+encodeURIComponent(state.id)+'/favorite',{favorite:value}); state.session.favorite = Number(value); readerCache.clear(); const button = $('#session-favorite'); button.innerHTML = icon('Star',value?'filled':''); button.classList.toggle('is-favorite',value); button.setAttribute('aria-label',value?'取消收藏':'收藏对话'); button.title = value?'取消收藏':'收藏对话'; toast(value?'已加入收藏':'已取消收藏'); await refreshStats(); await loadList(); } catch(error){handle(error);} finally { favoriteBusy = false; if($('#session-favorite')) ($('#session-favorite') as HTMLButtonElement).disabled = false; } }
function showLocalSearch() { state.localSearchOpen = true; $('#local-search').hidden = false; $('#session-search').setAttribute('aria-expanded','true'); input('#local-query').focus({preventScroll:true}); }
function closeLocalSearch() {
  hitAbort?.abort(); state.localSearchOpen = false; state.localQuery = ''; state.hits = []; state.hitIndex = 0;
  input('#local-query').value = ''; $('#local-search').hidden = true; $('#session-search').setAttribute('aria-expanded','false');
  $('#messages').querySelectorAll('.hit-message').forEach(message => message.classList.remove('hit-message'));
  updateHitCount(); markReader(); $('#session-search').focus({preventScroll:true});
}
async function findHits() { hitAbort?.abort(); hitAbort = new AbortController(); try { state.hits = await api('/sessions/'+encodeURIComponent(state.id)+'/hits?'+new URLSearchParams({q:state.localQuery,all:state.all?'1':''}),{signal:hitAbort.signal}); state.hitIndex = 0; updateHitCount(); markReader(); if(state.hits.length)await openSession(state.id,state.hits[0].id); } catch(error){handle(error);} }
function updateHitCount() { if(!$('#hit-count'))return; $('#hit-count').textContent = state.localQuery ? state.hits.length ? `${state.hitIndex+1} / ${state.hits.length}${state.hits.length===1000?'+':''}` : '无匹配' : ''; }
function stepHit(delta: number) { if(!state.hits.length)return; state.hitIndex = (state.hitIndex+delta+state.hits.length)%state.hits.length; openSession(state.id,state.hits[state.hitIndex].id); }
function sessionInfo() { const s = state.session!; $('#info-content').innerHTML = `<dl class="session-details"><dt>会话 ID</dt><dd>${esc(s.id)}</dd><dt>项目路径</dt><dd>${esc(s.cwd || 'ChatGPT 云端导出')}</dd><dt>应用 / 设备</dt><dd>${esc(s.provider)} / ${esc(s.platform)}</dd><dt>创建时间</dt><dd>${date(s.created,true)}</dd><dt>更新时间</dt><dd>${date(s.updated,true)}</dd><dt>消息与过程</dt><dd>${num(s.message_count)} 条正文 / ${num(s.total_count)} 条总记录</dd>${s.parent_id?`<dt>父会话</dt><dd><button class="text-button" id="parent-link">${esc(s.parent_id)}</button></dd>`:''}<dt>原始来源</dt><dd>${s.sources?.map(f=>`<p>${esc(f.path)}${f.missing?'<span class="phase-tag">源文件已删除 · 档案保留</span>':''}</p>`).join('')||'数据库历史 / ChatGPT 导出'}</dd></dl><div class="dialog-note">${icon('ShieldCheck')} 隐藏、收藏与导出仅作用于本地档案，原始对话保持只读。</div>`; if($('#parent-link'))$('#parent-link').onclick=()=>{(document.querySelector('#info-dialog') as HTMLDialogElement).close();openSession(s.parent_id!);}; (document.querySelector('#info-dialog') as HTMLDialogElement).showModal(); }

function debounce(fn: () => void, delay: number) { let timer = 0; return () => { clearTimeout(timer); timer = window.setTimeout(fn,delay); }; }
function closeSidebar() { document.body.classList.remove('nav-open'); }
function applySidebar() {
  const collapsed = localStorage.getItem('echo-sidebar-collapsed') === '1';
  const desktop = window.matchMedia('(min-width:901px)').matches;
  document.body.classList.toggle('sidebar-collapsed', collapsed);
  $('#archive-sidebar').inert = desktop && collapsed;
  const expanded = !desktop || !collapsed;
  const button = $('#sidebar-toggle');
  button.setAttribute('aria-expanded', String(expanded));
  button.setAttribute('aria-label', expanded ? '收起项目导航' : '展开项目导航');
  button.title = expanded ? '收起项目导航' : '展开项目导航';
  button.innerHTML = icon(expanded ? 'ChevronLeft' : 'ChevronRight');
}
$('#sidebar-toggle').onclick = () => {
  if (window.matchMedia('(max-width:900px)').matches) { closeSidebar(); return; }
  localStorage.setItem('echo-sidebar-collapsed', document.body.classList.contains('sidebar-collapsed') ? '0' : '1');
  applySidebar();
};
window.matchMedia('(min-width:901px)').addEventListener('change', applySidebar);
applySidebar();
function resetFilters() { state.platform='';state.provider='';state.from='';state.to='';state.all=false; ['#platform-filter','#provider-filter','#date-from','#date-to'].forEach(id => (document.querySelector(id) as HTMLInputElement).value='');input('#search-all').checked=false;$('#filter-dot').hidden=true;readerCache.clear(); }
async function sourcesDialog(open = true) {
  try { const [data, backup] = await Promise.all([api<{sources:Source[];chatgpt:string}>('/sources'), api<BackupStatus>('/backup/status')]); backupState = backup; const s = state.stats; const report=state.sync;
    $('#sources-summary').innerHTML = `<div><span>当前可见对话</span><b>${num(s?.sessions||0)}</b></div><div><span>档案大小</span><b>${size(s?.db_bytes||0)}</b></div><div><span>上次同步</span><b class="sync-date">${relative(s?.last_sync||0)}</b></div>`;
    $('#sources-content').innerHTML = `
      <div class="sources-grid">
        <section class="sources-column" aria-label="对话来源">
          <h3 class="section-label">${icon('Database')}对话来源 <span>${data.sources.length}</span></h3>
          <div class="source-list">${data.sources.map(source=>`<div class="source-row"><span class="source-icon">${icon(source.platform==='Windows'?'Monitor':source.kind==='ChatGPT'?'MessagesSquare':'Terminal')}</span><div><b>${esc(source.platform)} · ${esc(source.kind)}</b><code>${esc(source.path)}</code></div><span class="source-status ${source.exists?'':'unavailable'}"><i></i>${source.exists?'可读取':'暂不可用'}</span></div>`).join('') || '<p class="source-empty">暂无来源，添加路径或导入文件即可收录。</p>'}</div>
          <form id="add-source-form" class="path-form"><input id="source-path" aria-label="添加来源路径" placeholder=".codex、桌面 Chat 缓存或导出路径"><button class="button" type="submit">添加来源</button></form>
          <div class="dialog-note">${icon('MessagesSquare')}<span>自动发现 Windows 桌面应用的 Chat 正文缓存。只收录已缓存的内容；缺少历史时，在桌面应用打开对话并向前翻阅，再点击同步。缓存可能被清理，完整历史仍可通过官方导出补充。</span></div>
          <div class="import-section">
            <div><h3>导入 ChatGPT 导出文件</h3><p>支持官方 conversations.json / ZIP，保留全部对话分支，与桌面缓存按消息 ID 合并。</p><a href="https://help.openai.com/en/articles/7260999-exporting-your-chatgpt-history-and-data" target="_blank" rel="noopener noreferrer">如何导出 ChatGPT 数据 ${icon('ArrowUpRight')}</a></div>
            <label class="button import-label">${icon('Upload')}导入文件<input id="import-file" type="file" accept=".json,.zip" hidden></label>
          </div>
          ${report&&(report.errors.length||report.warnings.length)?`<details class="sync-issues"><summary>${icon('CircleHelp')}${report.errors.length} 个错误 · ${report.warnings.length} 个提示</summary>${[...report.errors,...report.warnings].map(e=>`<p><code>${esc(e.path)}</code><span>${esc(e.error)}</span></p>`).join('')}</details>`:''}
        </section>
        <section class="backup-section sources-column" aria-label="完整档案备份">
          <h3 class="section-label">${icon('Download')}完整档案备份</h3>
          <p class="backup-description">包含所有正文、收藏、隐藏状态、索引、原始事件和已收录附件。</p>
          <label class="field-caption" for="backup-directory">保存目录</label>
          <form id="backup-directory-form" class="path-form"><input id="backup-directory" aria-label="备份保存目录" value="${esc(backup.directory)}" placeholder="D:/备份/EchoVault"><button type="submit" class="button">记住位置</button></form>
          <p class="backup-directory-note">支持 Windows 路径；目录不存在时自动创建。</p>
          <label class="backup-format">备份格式<select id="backup-format" aria-label="备份格式"><option value="zstd">Zstandard · 快速压缩（推荐）</option><option value="zstd-small">Zstandard · 更小体积（较慢）</option><option value="sqlite">SQLite · 不压缩</option></select></label>
          <div class="backup-actions"><button id="backup-local" class="button primary">${icon('Folder')}备份到此目录</button><button id="backup-link" class="button">${icon('Download')}备份整个档案 · 下载</button></div>
          <p class="backup-description">无损压缩，完整保留数据。直接保存无需浏览器传输，每次生成新文件，保留已有备份。</p>
          <div id="backup-progress" class="backup-progress" hidden role="status" aria-live="polite"></div>
        </section>
        <section class="restore-section sources-column" aria-label="恢复完整档案">
          <h3 class="section-label">${icon('Upload')}恢复完整档案</h3>
          <p class="backup-description">支持 .sqlite / .sqlite.zst 完整备份，包含全部项目、对话、附件及隐藏状态。</p>
          <label class="field-caption" for="restore-path">备份文件路径</label>
          <form id="restore-path-form" class="path-form"><input id="restore-path" aria-label="恢复备份路径" placeholder="D:/备份/echo.sqlite.zst"><button id="restore-path-button" class="button" type="submit">从路径恢复</button></form>
          <p class="backup-directory-note">大文件推荐本机路径，无需浏览器上传。</p>
          <div class="restore-upload"><span>也可以上传备份文件</span><label class="button import-label">${icon('Upload')}选择完整备份<input id="restore-file" type="file" accept=".sqlite,.sqlite3,.db,.zst" hidden></label></div>
          <p class="backup-description">恢复到独立档案，当前文件继续保留。检查通过后，按提示重启服务切换。</p>
          <div id="restore-progress" class="backup-progress" hidden role="status" aria-live="polite"></div>
        </section>
      </div>
      <div class="sources-footer"><div class="preservation-note">${icon('ShieldCheck')}<div><b>原记录只读，档案由你管理</b><p>原文件删除、会话归档或来源断开，档案仍保留；主动删除的对话仅在完整重新扫描时可重新收录。</p></div></div><button id="full-sync" class="text-button">${icon('RefreshCw')}完整重新扫描</button></div>`;
    $('#add-source-form').onsubmit=async e=>{e.preventDefault();try{await post('/sources',{path:input('#source-path').value});toast('来源已添加，点击同步收录记录');await sourcesDialog(false);}catch(error){handle(error);}};
    input('#import-file').onchange=async()=>{const file=input('#import-file').files?.[0];if(!file)return;try{toast('正在导入 '+file.name);await api('/import',{method:'POST',headers:{'X-Echo-Request':'1','X-Filename':encodeURIComponent(file.name)},body:file});toast('文件已导入，正在收录对话');pollSync();}catch(error){handle(error);}};
    $('#full-sync').onclick=()=>{if(window.confirm('完整重新扫描会重新收录已删除的对话（原始来源仍可读取时）。继续扫描？'))startSync(true);};
    $('#backup-link').onclick=()=>startArchiveBackup('download');
    $('#restore-path-form').onsubmit=e=>{e.preventDefault();startArchiveRestore();};
    input('#restore-file').onchange=()=>{const file=input('#restore-file').files?.[0];if(file)startArchiveRestore(file);};
    renderRestore(); pollRestore();
    $('#backup-local').onclick=()=>startArchiveBackup('local');
    $('#backup-directory-form').onsubmit=async e=>{e.preventDefault();try{await saveBackupDirectory();toast('备份位置已记住');}catch(error){handle(error);}};
    renderBackup();
    pollBackup();
    if(open)(document.querySelector('#sources-dialog') as HTMLDialogElement).showModal();
  }catch(error){handle(error);}
}

function backupRunning() { return backupStarting || backupState?.job?.stage === 'copying' || backupState?.job?.stage === 'compressing' || backupState?.job?.stage === 'finishing'; }
function renderBackup() {
  const busy = backupRunning(), job = backupState?.job;
  for (const id of ['#backup-local', '#backup-link']) {
    const button = document.querySelector<HTMLButtonElement>(id);
    if (button) { button.disabled = busy; button.setAttribute('aria-busy', String(busy)); }
  }
  const progress = document.querySelector<HTMLElement>('#backup-progress');
  const activity = $('#backup-activity');
  activity.hidden = !busy;
  let content = '';
  if (backupStarting) content = `${icon('LoaderCircle','spinning')} 正在启动完整备份…`;
  else if (job) {
    const seconds = Math.max(0, Math.floor(job.duration));
    const percent = job.total_bytes ? Math.floor(job.done_bytes / job.total_bytes * 100) : 0;
    if (busy) {
      const stage = job.stage === 'compressing' ? '正在无损压缩完整快照' : job.stage === 'finishing' ? '正在完成文件写入' : '正在备份整个档案';
      content = `<div class="backup-progress-heading">${icon('LoaderCircle','spinning')}<b>${stage}</b><span>${seconds} 秒</span></div><progress max="100" ${job.total_bytes ? `value="${percent}"` : ''} aria-label="完整备份进度"></progress><p>${job.total_bytes ? `${percent}% · ${size(job.done_bytes)} / ${size(job.total_bytes)}` : '正在准备档案快照…'} · 可以继续阅读</p>`;
    } else if (job.stage === 'ready') {
      content = `<div class="backup-progress-heading">${icon('CheckCircle2')}<b>${job.mode === 'local' ? '完整备份已保存' : '完整备份已生成'}</b></div><p>${size(job.output_bytes || job.total_bytes)}${job.input_bytes && job.format !== 'sqlite' ? ` · 原始 ${size(job.input_bytes)} · 节省 ${Math.round((1-(job.output_bytes || job.total_bytes)/job.input_bytes)*100)}%` : ''} · 耗时 ${seconds} 秒</p>${job.mode === 'local' ? `<code>${esc(job.path)}</code>` : `<a class="button" href="/api/backup/download?id=${encodeURIComponent(job.id)}" download>${icon('Download')}保存备份文件</a><p>可点击保存完整文件。下载文件保留 1 小时，开始新备份后会替换。</p>`}`;
    } else if (job.stage === 'error') content = `${icon('CircleHelp')}<b>备份失败，可以重试</b><p>${esc(job.error)}</p>`;
    else content = '下载备份已过期，请重新生成。';
  }
  if (progress) { progress.hidden = !content; progress.innerHTML = content; progress.classList.toggle('backup-error', job?.stage === 'error'); }
  if (busy) activity.innerHTML = content;
}
async function saveBackupDirectory() {
  const result = await post<{directory:string}>('/backup/settings', {directory: input('#backup-directory').value});
  input('#backup-directory').value = result.directory;
  if (backupState) backupState.directory = result.directory;
}
async function startArchiveBackup(mode: 'local' | 'download') {
  if (backupRunning()) return;
  backupStarting = true;
  renderBackup();
  try {
    if (mode === 'local') await saveBackupDirectory();
    const job = await post<BackupJob>('/backup', {mode, format: (document.querySelector('#backup-format') as HTMLSelectElement).value});
    backupState = {directory: backupState?.directory || '', job};
    sessionStorage.setItem('echo-backup-pending', job.id);
  } catch (error) { handle(error); }
  finally { backupStarting = false; renderBackup(); pollBackup(); }
}
async function pollBackup() {
  if (backupPolling) return;
  clearTimeout(backupTimer);
  backupPolling = true;
  try {
    const before = backupState?.job;
    backupState = await api<BackupStatus>('/backup/status');
    renderBackup();
    const job = backupState.job;
    if (job?.stage === 'ready' && sessionStorage.getItem('echo-backup-notified') !== job.id) {
      sessionStorage.setItem('echo-backup-notified', job.id);
      if (job.mode === 'download' && sessionStorage.getItem('echo-backup-pending') === job.id) {
        const link = document.createElement('a');
        link.href = '/api/backup/download?id=' + encodeURIComponent(job.id); link.download = ''; document.body.append(link); link.click(); link.remove();
        toast('完整备份已生成，已请求浏览器保存');
      } else if (job.mode === 'local') toast('完整备份已保存到指定目录');
      sessionStorage.removeItem('echo-backup-pending');
    } else if (job?.stage === 'error' && before?.stage !== 'error') handle(new Error('备份失败：' + job.error));
  } catch {
    if (backupRunning()) {
      const message = `${icon('LoaderCircle','spinning')} 暂时无法获取备份进度，正在重新连接…`;
      $('#backup-activity').innerHTML = message;
      const progress = document.querySelector<HTMLElement>('#backup-progress');
      if (progress) progress.innerHTML = message;
    }
  } finally {
    backupPolling = false;
    backupTimer = window.setTimeout(pollBackup, backupRunning() ? 700 : 8000);
  }
}
pollBackup();

interface RestoreJob { id: string; stage: string; duration: number; done_bytes: number; total_bytes: number; path?: string; command?: string; error?: string }
let restoreJob: RestoreJob | null = null, restoreStarting = false, restorePolling = false, restoreTimer = 0;
function restoreRunning() { return restoreStarting || !!restoreJob && ['uploading','restoring','validating'].includes(restoreJob.stage); }
function renderRestore() {
  const busy = restoreRunning();
  for (const selector of ['#restore-path-button', '#restore-file']) {
    const element = document.querySelector<HTMLInputElement | HTMLButtonElement>(selector);
    if (element) element.disabled = busy;
  }
  const progress = document.querySelector<HTMLElement>('#restore-progress');
  if (!progress) return;
  progress.hidden = !restoreStarting && !restoreJob;
  if (restoreStarting) { progress.innerHTML = `${icon('LoaderCircle','spinning')} 正在上传 / 启动恢复…`; return; }
  const job = restoreJob;
  if (!job) return;
  if (busy) {
    const percent = job.total_bytes ? Math.min(100, Math.floor(job.done_bytes / job.total_bytes * 100)) : 0;
    progress.innerHTML = `<b>${icon('LoaderCircle','spinning')} ${job.stage === 'validating' ? '正在检查完整性' : job.stage === 'uploading' ? '正在上传备份' : '正在恢复完整档案'}</b><progress max="100" ${job.stage !== 'validating' && job.total_bytes ? `value="${percent}"` : ''}></progress><p>${job.stage !== 'validating' && job.total_bytes ? `${percent}% · ${size(job.done_bytes)} / ${size(job.total_bytes)} · ` : ''}${Math.floor(job.duration)} 秒 · 可以继续阅读</p>`;
  } else if (job.stage === 'ready') {
    progress.innerHTML = `<b>${icon('CheckCircle2')} 完整档案已恢复，检查通过</b><code>${esc(job.path)}</code><p>停止当前服务，然后在项目目录执行下面的命令，切换到恢复的档案：</p><code>${esc(job.command)}</code><button id="copy-restore-command" class="text-button">${icon('Copy')}复制启动命令</button><p>恢复是完整档案切换，不会合并进当前档案。当前数据文件仍然保留。</p>`;
    $('#copy-restore-command').onclick = () => navigator.clipboard.writeText(job.command || '').then(()=>toast('启动命令已复制')).catch(handle);
  } else progress.innerHTML = `<b>恢复失败，可以重试</b><p>${esc(job.error)}</p>`;
}
async function startArchiveRestore(file?: File) {
  if (restoreRunning()) return;
  restoreStarting = true; renderRestore();
  try {
    const result = file ? await api<{job:RestoreJob}>('/restore/upload', {method:'POST', headers:{'X-Echo-Request':'1'}, body:file}) : await post<{job:RestoreJob}>('/restore', {path:input('#restore-path').value});
    restoreJob = result.job; toast('恢复任务已启动，可继续阅读');
  } catch(error) { handle(error); }
  finally { restoreStarting = false; renderRestore(); pollRestore(); }
}
async function pollRestore() {
  if (restorePolling) return;
  clearTimeout(restoreTimer); restorePolling = true;
  try {
    restoreJob = (await api<{job:RestoreJob|null}>('/restore/status')).job;
    renderRestore();
  } catch(error) { if (restoreRunning()) handle(error); }
  finally {
    restorePolling = false;
    if (restoreRunning()) restoreTimer = window.setTimeout(pollRestore, 1000);
  }
}
pollRestore();

async function startSync(full = false, automatic = false) {
  if(syncStarting || state.sync?.running)return;
  syncStarting = true;
  try {
    const result=await post<{started:boolean}>('/sync',automatic?{full:false,automatic:true}:{full});
    if(result.started){if(!automatic)toast(full?'开始完整扫描，已有历史继续保留':'开始同步对话');readerCache.clear();}
    void pollSync();
  }catch(error){if(!automatic)handle(error);}
  finally{syncStarting=false;}
}
function maybeAutoSync() {
  const report=state.sync;
  if(!report || report.running || syncStarting)return;
  if(report.started==null && report.finished==null)return;
  const last=Math.max(report.started||0,report.finished||0)*1000;
  const current=Date.now();
  if((!last || current-last>=automaticSyncInterval)&&current-lastAutoSyncAttempt>=60000){lastAutoSyncAttempt=current;void startSync(false,true);}
}
async function pollSync() {
  window.clearTimeout(syncTimer);
  if(syncBusy)return;syncBusy=true;
  let checked = false;
  try { const previous=state.sync;const report=await api<Sync>('/sync');state.sync=report;checked=true;
    const button=$('#sync-button');button.classList.toggle('syncing',report.running);button.querySelector('span')!.textContent=report.running?'正在同步':'同步对话';(button as HTMLButtonElement).disabled=report.running;
    if(report.running){$('#sync-status').innerHTML=`${icon('RefreshCw','spinning')} 正在收录 ${report.done} / ${report.total} <span class="sync-file">${esc(report.file)}</span>`;if(report.done%10===0)refreshStats().catch(handle);}
    else {$('#sync-status').innerHTML=`<i class="status-dot"></i> ${report.finished?`${report.errors.length?'同步完成，部分来源待重试':'档案已更新'} · ${num(report.imported)} 条新增 / 更新 · ${report.duration}s`:'准备就绪'}`;if(previous&&(previous.running||report.finished!==previous.finished)){readerCache.clear();await refreshStats();await loadList();if(!report.automatic||report.errors.length)toast(report.errors.length?'同步完成，部分来源未读取；详情见数据与来源':`同步完成 · ${num(report.imported)} 条新增或更新`);if((document.querySelector('#sources-dialog') as HTMLDialogElement).open)sourcesDialog(false);}}
  }catch(error){if(!state.sync)$('#sync-status').innerHTML='<i class="status-dot offline"></i> 服务暂时未连接';}
  finally{syncBusy=false;syncTimer=window.setTimeout(pollSync,state.sync?.running?1200:8000);if(checked)maybeAutoSync();}
}
document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')void pollSync();});
window.addEventListener('focus',()=>void pollSync());

input('#global-search').addEventListener('input',debounce(()=>{state.q=input('#global-search').value.trim();saveUrl();loadList();},170));
$('#clear-search').onclick=()=>{state.q='';input('#global-search').value='';saveUrl();loadList();input('#global-search').focus();};
input('#project-search').oninput=renderProjects;
$('#show-hidden').onclick=()=>changeVisibilityScope().catch(handle);
renderVisibilityToggle();
document.querySelectorAll<HTMLElement>('[data-nav]').forEach(el=>el.onclick=()=>{state.project='';state.favorite=el.dataset.nav==='favorite';updateNav();saveUrl();loadList();closeSidebar();});
$('#sort').onchange=()=>{state.sort=(document.querySelector('#sort') as HTMLSelectElement).value;loadList();};
$('#filter-button').onclick=()=>{const hidden=!$('#filters').hidden;$('#filters').hidden=hidden;$('#filter-button').setAttribute('aria-expanded',String(!hidden));};
for(const selector of ['#platform-filter','#provider-filter','#date-from','#date-to','#search-all'])$(selector).onchange=()=>{state.platform=(document.querySelector('#platform-filter') as HTMLSelectElement).value;state.provider=(document.querySelector('#provider-filter') as HTMLSelectElement).value;state.from=input('#date-from').value;state.to=input('#date-to').value;state.all=input('#search-all').checked;$('#filter-dot').hidden=!(state.platform||state.provider||state.from||state.to||state.all);saveUrl();loadList();};
$('#reset-filters').onclick=()=>{resetFilters();saveUrl();loadList();};
$('#sources-button').onclick=()=>sourcesDialog();$('#sync-button').onclick=()=>startSync();
$('#sync-button').title='页面打开时约每5分钟自动增量同步；点击立即同步';
$('#theme').onclick=()=>{const theme=document.documentElement.dataset.theme==='dark'?'light':'dark';document.documentElement.dataset.theme=theme;localStorage.setItem('echo-theme',theme);$('#theme').innerHTML=icon(theme==='dark'?'Sun':'Moon');};
$('#theme').innerHTML=icon(document.documentElement.dataset.theme==='dark'?'Sun':'Moon');
$('#mobile-menu').onclick=()=>document.body.classList.toggle('nav-open');$('.sidebar-scrim').onclick=closeSidebar;
document.querySelectorAll<HTMLDialogElement>('dialog').forEach(dialog=>{dialog.querySelector('[data-close-dialog]')!.addEventListener('click',()=>dialog.close());dialog.addEventListener('click',e=>{if(dialog.id==='delete-session-dialog'&&deleteBusy)return;if(e.target===dialog){const r=dialog.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)dialog.close();}});});
document.addEventListener('keydown',e=>{
  const editing=(e.target as HTMLElement).matches('input,textarea,select,[contenteditable]');
  if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='k'){e.preventDefault();input('#global-search').focus();input('#global-search').select();}
  if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='f'&&state.session){e.preventDefault();showLocalSearch();}
  if(e.key==='Escape'&&!document.querySelector('dialog[open]')){closeSidebar();if($('#local-search')&&!$('#local-search').hidden){closeLocalSearch();}else if(state.q){state.q='';input('#global-search').value='';saveUrl();loadList();}}
  if(!editing&&!document.querySelector('dialog[open]')&&['ArrowDown','ArrowUp'].includes(e.key)){e.preventDefault();const index=state.items.findIndex(s=>s.id===state.id);const next=Math.max(0,Math.min(state.items.length-1,index+(e.key==='ArrowDown'?1:-1)));if(state.items[next]){openSession(state.items[next].id,undefined,true);document.querySelector<HTMLElement>(`.conversation-card[data-id="${CSS.escape(state.items[next].id)}"]`)?.scrollIntoView({block:'nearest'});}}
});
window.onpopstate=()=>{const next=new URL(location.href);state.q=next.searchParams.get('q')||'';state.project=next.searchParams.get('project')||'';state.favorite=next.searchParams.get('favorite')==='1';state.platform=next.searchParams.get('platform')||'';state.showHidden=next.searchParams.get('hidden')==='1';renderVisibilityToggle();refreshStats().catch(handle);input('#global-search').value=state.q;updateNav();loadList();const id=next.searchParams.get('session');if(id)openSession(id);else{state.id='';state.session=null;welcome();document.body.classList.remove('reading');}};
$('#conversation-list').addEventListener('scroll',()=>{const list=$('#conversation-list');if(state.more&&!state.loading&&list.scrollHeight-list.scrollTop-list.clientHeight<200)loadList(true);},{passive:true});

welcome();
Promise.all([refreshStats(),loadList()]).then(()=>{updateNav();if(state.id)openSession(state.id);}).catch(handle);
pollSync();
