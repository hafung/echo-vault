import { marked } from 'marked';
import DOMPurify from 'dompurify';
import { staticValue, type Scope } from './dil-values';

type Data = Record<string, unknown>;
export interface ChatGPTUI {
  model_dil_v2?: { code?: string; constants?: Data; fallbackMarkdown?: string; appData?: { opGenui?: { componentResults?: Data; modelDataBindings?: Data } } };
  genui_components?: { type?: string; component_resolution_id?: string }[];
  content_references?: Data[];
  search_result_groups?: Data[];
}
interface Element { name: string; attrs: string; children: Part[] }
type Part = string | Element;
const containers = new Set(['box', 'row', 'column', 'grid', 'grid-item', 'text', 'title', 'caption', 'badge', 'bold', 'italic', 'link', 'list', 'list-item', 'table', 'table-head', 'table-body', 'table-row', 'table-cell', 'button', 'form', 'radio-group', 'radio', 'checkbox', 'select', 'option', 'accordion', 'accordion-item', 'callout', 'card', 'tabs', 'tab', 'svg', 'g', 'appblock']);
const singles = new Set(['asyncimage', 'asyncimagegroup', 'entity', 'cite', 'memorycite', 'icon', 'divider', 'separator', 'image', 'slider', 'input', 'progress', 'chart', 'bar-chart', 'line-chart', 'pie-chart', 'map', 'rect', 'path', 'circle', 'ellipse', 'line', 'polyline', 'polygon']);
const inline = new Set(['entity', 'cite', 'memorycite', 'icon', 'badge', 'bold', 'italic', 'link']);
const esc = (value: unknown) => String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const data = (value: unknown): Data => value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
const text = (value: unknown) => typeof value === 'string' || typeof value === 'number' ? String(value) : '';
export function safeRemoteURL(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  try { const u = new URL(value); return ['https:', 'http:'].includes(u.protocol) && !u.username && !u.password ? u.href : null; } catch { return null; }
}

// JSX attributes can contain strings, nested objects and arrows with `>` in them.
function balancedEnd(source: string, start: number, tag: boolean): number {
  let braces = 0, quote = '';
  for (let i = start; i < source.length; i++) {
    const c = source[i];
    if (quote) { if (c === '\\') i++; else if (c === quote) quote = ''; continue; }
    if (c === '"' || c === "'" || c === '`') quote = c;
    else if (c === '{') braces++;
    else if (c === '}') { if (--braces === 0 && !tag) return i + 1; }
    else if (c === '>' && braces === 0 && tag) return i + 1;
  }
  return -1;
}

function parts(source: string): Part[] {
  const root: Element = { name: '', attrs: '', children: [] }, stack = [root];
  const lastIndex = (name: string) => { for (let i = stack.length - 1; i > 0; i--) if (stack[i].name === name) return i; return -1; };
  const append = (value: Part) => { const children = stack.at(-1)!.children; if (typeof value === 'string' && typeof children.at(-1) === 'string') children[children.length - 1] += value; else children.push(value); };
  for (let i = 0; i < source.length;) {
    if (source[i] === '\\' && i + 1 < source.length) { append(source.slice(i, i + 2)); i += 2; continue; }
    // Leave both fenced and inline code completely opaque to DIL parsing.
    const fence = (i === 0 || source[i - 1] === '\n') && /^ {0,3}(`{3,}|~{3,})[^\n]*(?:\n|$)/.exec(source.slice(i));
    if (fence) {
      const close = new RegExp('^ {0,3}' + fence[1][0] + '{' + fence[1].length + ',}[ \\t]*(?:\\n|$)', 'm');
      const rest = source.slice(i + fence[0].length), match = close.exec(rest);
      const end = match ? i + fence[0].length + match.index + match[0].length : source.length;
      append(source.slice(i, end)); i = end; continue;
    }
    if (source[i] === '`') {
      const run = /^`+/.exec(source.slice(i))![0], end = source.indexOf(run, i + run.length);
      if (end >= 0) { append(source.slice(i, end + run.length)); i = end + run.length; continue; }
    }
    if (source[i] === '<') {
      const match = /^<(\/?)([A-Za-z][\w-]*)(?=[\s/>])/.exec(source.slice(i));
      const name = match?.[2].toLowerCase();
      if (match && name && (containers.has(name) || singles.has(name))) {
        const end = balancedEnd(source, i, true);
        if (end < 0) { append(esc(source.slice(i))); break; }
        const raw = source.slice(i, end);
        if (match[1]) {
          const index = lastIndex(name);
          if (index > 0) stack.length = index;
        } else {
          const element: Element = { name, attrs: raw.slice(match[0].length).replace(/\/?\s*>$/, ''), children: [] };
          append(element);
          if (!/\/\s*>$/.test(raw) && !singles.has(name)) stack.push(element);
        }
        i = end; continue;
      }
    }
    if (source[i] === '{' && /^\{(?:@body\b|#(?:each|if)\b|\/(?:each|if)\b|:else\b)/.test(source.slice(i))) {
      const end = balancedEnd(source, i, false);
      if (end < 0) { append(esc(source.slice(i))); break; }
      const directive = source.slice(i + 1, end - 1);
      if (/^\//.test(directive)) {
        const index = lastIndex(directive.slice(1).trim());
        if (index > 0) stack.length = index;
      } else if (directive.startsWith(':else')) {
        const index = lastIndex('if');
        if (index > 0) { stack.length = index + 1; const el: Element = { name: 'else', attrs: directive.slice(5).trim(), children: [] }; append(el); stack.push(el); }
      } else {
        const [name] = directive.slice(1).split(/\s/), el: Element = { name, attrs: directive.slice(name.length + 1).trim(), children: [] };
        append(el); if (name !== 'body') stack.push(el);
      }
      i = end; continue;
    }
    append(source[i++]);
  }
  return root.children;
}

function attributes(source: string, scope: Scope): Data {
  const result = Object.create(null) as Data;
  for (let i = 0; i < source.length;) {
    const match = /^\s*([\w-]+)\s*(=\s*)?/.exec(source.slice(i));
    if (!match) break;
    i += match[0].length; const name = match[1];
    if (!match[2]) { result[name] = true; continue; }
    const start = i, c = source[i];
    if (c === '{') { const end = balancedEnd(source, i, false); if (end < 0) break; result[name] = staticValue(source.slice(i + 1, end - 1), scope); i = end; }
    else if (c === '"' || c === "'") { i++; while (i < source.length && source[i] !== c) { if (source[i] === '\\') i++; i++; } i++; result[name] = staticValue(source.slice(start, i), scope); }
    else { while (i < source.length && !/\s/.test(source[i])) i++; result[name] = source.slice(start, i); }
  }
  return result;
}

function dedent(source: string): string {
  const lines = source.replace(/^\s*\n/, '').split('\n');
  const indents = lines.filter(line => line.trim()).map(line => /^ */.exec(line)![0].length);
  const amount = Math.min(...indents, 0x7fffffff);
  return lines.map(line => line.slice(Math.min(amount, /^ */.exec(line)![0].length))).join('\n').trim();
}

export function renderMessageContent(body: string, ui?: ChatGPTUI, chatGPT = false): string {
  const sanitize = (html: string) => DOMPurify.sanitize(html, { FORBID_TAGS: ['img', 'style', 'iframe'], FORBID_ATTR: ['style'] });
  const dil = ui?.model_dil_v2;
  if (!dil && !(chatGPT && /<\/?(?:box|row|grid|grid-item|text|title|caption|badge|AsyncImage|AsyncImageGroup|Entity|Cite|Link|AppBlock)\b|\{(?:@body|#each|#if)\b/.test(body))) return sanitize(marked.parse(body, { async: false }) as string);
  const tree = parts(body || dil?.fallbackMarkdown || '');
  // Merely mentioning DIL inside a Markdown code example doesn't change modes.
  if (!tree.some(node => typeof node !== 'string')) return sanitize(marked.parse(body || dil?.fallbackMarkdown || '', { async: false }) as string);
  const resolved = dil?.appData?.opGenui?.componentResults ?? {};
  const order: string[] = [];
  for (const match of (dil?.code ?? '').matchAll(/"__resolutionId"\s*:\s*__dilConstants\["([^"]+)"\]/g)) {
    const id = text(dil?.constants?.[match[1]]); if (id && !order.includes(id)) order.push(id);
  }
  if (!order.length) for (const component of ui?.genui_components ?? []) if (component.component_resolution_id) order.push(component.component_resolution_id);
  const ids = order.length ? order : Object.keys(resolved);
  const queues = new Map<string, Data[]>();
  for (const id of ids) {
    const component = data(resolved[id]), name = text(component.componentName).toLowerCase();
    if (name) queues.set(name, [...(queues.get(name) ?? []), data(component.state)]);
  }
  const cursors = new Map<string, number>();
  const getState = (name: string) => { const index = cursors.get(name) ?? 0; cursors.set(name, index + 1); return queues.get(name)?.[index] ?? {}; };
  const scope = Object.assign(Object.create(null), dil?.appData?.opGenui?.modelDataBindings ?? {}) as Scope;
  let limited = false, count = 0;
  function markdown(source: string, context: Scope): string {
    // Only interpolate outside code spans/fences; parts() has already preserved them.
    source = source.replace(/(`{3,}[^\n]*\n[\s\S]*?(?:\n`{3,}|$)|~{3,}[^\n]*\n[\s\S]*?(?:\n~{3,}|$)|(`+)[\s\S]*?\2)|\{([^{}\n]+)\}/g, (raw, code: string | undefined, _ticks: string, expression: string) => {
      if (code) return raw;
      const value = staticValue(expression, context);
      if (value === undefined || typeof value === 'object') { limited = true; return '<code>' + esc(raw) + '</code>'; }
      return esc(value).replace(/([\\`*_[\]#])/g, '\\$1');
    });
    return marked.parse(source, { async: false }) as string;
  }
  function children(nodes: Part[], context: Scope, nested = true, depth = 0): string {
    if (depth > 40 || count > 5000) { limited = true; return '<p>内容较长，请查看原始内容。</p>'; }
    const slots: string[] = [];
    let source = '';
    for (const node of nodes) {
      if (typeof node === 'string') source += node;
      else {
        count++;
        const html = element(node, context, depth + 1);
        const marker = '\uE000EV' + slots.length + '\uE001';
        slots.push(html);
        source += marker;
      }
    }
    source = nested ? dedent(source) : source;
    const elements = nodes.filter((node): node is Element => typeof node !== 'string');
    source = source.replace(/\uE000EV(\d+)\uE001/g, (marker, index: string) => inline.has(elements[Number(index)].name) ? marker : '\n\n' + marker + '\n\n');
    let html = markdown(source, context);
    html = html.replace(/<p>(\uE000EV\d+\uE001)<\/p>/g, '$1');
    return html.replace(/\uE000EV(\d+)\uE001/g, (_, index: string) => slots[Number(index)] ?? '');
  }
  function image(attrs: Data, state: Data): string {
    const images = Array.isArray(state.images) ? state.images : [attrs];
    return images.slice(0, 12).map(value => {
      const item = data(value), url = safeRemoteURL(item.thumbnail_url ?? item.content_url ?? item.src ?? item.url);
      const label = text(item.title ?? attrs.alt ?? attrs.query) || '回答中的图片';
      const source = safeRemoteURL(item.url);
      return `<figure class="dil-image"><div class="dil-image-preview">${url ? `<button type="button" class="dil-load-image" data-image-url="${esc(url)}" data-image-alt="${esc(label)}">加载图片</button>` : '<span>图片未包含在存档中</span>'}</div><figcaption>${esc(label)}${source ? ` <a href="${esc(source)}">图片来源</a>` : ''}</figcaption></figure>`;
    }).join('');
  }
  function vector(nodes: Part[], context: Scope, depth: number): string {
    if (depth > 40 || count > 5000) { limited = true; return ''; }
    const allowed = new Set(['svg', 'g', 'text', 'title', 'rect', 'path', 'circle', 'ellipse', 'line', 'polyline', 'polygon']);
    const props = new Set(['viewBox', 'width', 'height', 'x', 'y', 'x1', 'y1', 'x2', 'y2', 'cx', 'cy', 'r', 'rx', 'ry', 'd', 'points', 'fill', 'fillOpacity', 'stroke', 'strokeWidth', 'strokeOpacity', 'strokeLinecap', 'strokeLinejoin', 'fontSize', 'fontWeight', 'textAnchor', 'opacity', 'transform']);
    return nodes.map(node => {
      if (typeof node === 'string') return esc(node.replace(/\{([^{}]+)\}/g, (raw, expression: string) => text(staticValue(expression, context)) || raw));
      count++;
      if (node.name === 'each') {
        const match = /^([\s\S]+)\s+as\s+([\w$]+)(?:\s*,\s*([\w$]+))?$/.exec(node.attrs), values = match && staticValue(match[1], context);
        if (match && Array.isArray(values)) return values.slice(0, 100).map((value, index) => vector(node.children, Object.assign(Object.create(null), context, { [match[2]]: value, ...(match[3] ? { [match[3]]: index } : {}) }), depth + 1)).join('');
        limited = true; return vector(node.children, context, depth + 1);
      }
      if (node.name === 'if') {
        const condition = staticValue(node.attrs, context);
        if (condition === undefined) limited = true;
        const other = node.children.find(part => typeof part !== 'string' && part.name === 'else') as Element | undefined;
        return vector(condition !== undefined && !condition ? other?.children ?? [] : node.children.filter(part => part !== other), context, depth + 1);
      }
      if (!allowed.has(node.name)) { limited = true; return vector(node.children, context, depth + 1); }
      const attrs = attributes(node.attrs, context);
      const serialized = Object.entries(attrs).filter(([key, value]) => props.has(key) && (typeof value === 'string' || typeof value === 'number') && !/url\s*\(/i.test(String(value))).map(([key, value]) => ` ${key === 'viewBox' ? key : key.replace(/[A-Z]/g, c => '-' + c.toLowerCase())}="${esc(value)}"`).join('');
      return `<${node.name}${serialized}>${vector(node.children, context, depth + 1)}</${node.name}>`;
    }).join('');
  }
  function element(node: Element, context: Scope, depth: number): string {
    const a = attributes(node.attrs, context), name = node.name;
    if (name === 'svg') return vector([node], context, depth);
    if (name === 'body') {
      const state = /^const\s+\[\s*([\w$]+)\s*,[^\]]+\]\s*=\s*DIL\.useState\(([\s\S]*)\)\s*;?$/.exec(node.attrs);
      const variable = /^(?:const|let)\s+([\w$]+)\s*=\s*([\s\S]*?)\s*;?$/.exec(node.attrs);
      if (state) context[state[1]] = staticValue(state[2], context);
      else if (variable) context[variable[1]] = staticValue(variable[2], context);
      else limited = true;
      return '';
    }
    if (name === 'each') {
      const match = /^([\s\S]+)\s+as\s+([\w$]+)(?:\s*,\s*([\w$]+))?$/.exec(node.attrs);
      const values = match && staticValue(match[1], context);
      if (match && Array.isArray(values)) return values.slice(0, 100).map((value, index) => children(node.children, Object.assign(Object.create(null), context, { [match[2]]: value, ...(match[3] ? { [match[3]]: index } : {}) }), true, depth)).join('');
      limited = true; return children(node.children, context, true, depth);
    }
    if (name === 'if') {
      const condition = staticValue(node.attrs, context), other = node.children.find(part => typeof part !== 'string' && part.name === 'else') as Element | undefined;
      if (condition === undefined) limited = true;
      if (condition !== undefined && !condition) return other ? other.attrs.startsWith('if ') ? element({ ...other, name: 'if', attrs: other.attrs.slice(3) }, context, depth) : children(other.children, context, true, depth) : '';
      return children(node.children.filter(part => part !== other), context, true, depth);
    }
    if (name === 'else') return '';
    if (name === 'asyncimage' || name === 'asyncimagegroup' || name === 'image') return image(a, getState(name));
    if (name === 'entity') { const state = getState(name); return `<span class="dil-entity">${esc(state.value ?? a.value ?? a.name ?? '实体')}</span>`; }
    if (name === 'cite' || name === 'memorycite') {
      const state = getState(name), sources = Array.isArray(state.items) ? state.items : Array.isArray(state.citations) ? state.citations : Array.isArray(state.sources) ? state.sources : [];
      const urls: { url: string; title: string }[] = [];
      const refs = Array.isArray(a.refs) ? a.refs : [];
      for (const value of sources) { const item = data(value), url = safeRemoteURL(item.url); if (url) urls.push({ url, title: text(item.title) || '来源' }); }
      for (const group of ui?.search_result_groups ?? []) for (const value of Array.isArray(group.entries) ? group.entries : Array.isArray(group.search_results) ? group.search_results : []) {
        const item = data(value), url = safeRemoteURL(item.url);
        const ref = data(item.ref_id), id = text(item.ref_id ?? item.ref) || (ref.turn_index !== undefined ? `turn${ref.turn_index}${ref.ref_type}${ref.ref_index}` : '');
        if (url && refs.includes(id)) urls.push({ url, title: text(item.title) || '来源' });
      }
      return urls.length ? `<span class="dil-citations">${urls.filter((v, i, all) => all.findIndex(other => other.url === v.url) === i).map((v, i) => `<a href="${esc(v.url)}" title="${esc(v.title)}">[${i + 1}]</a>`).join(' ')}</span>` : `<span class="dil-citations" title="${esc(refs.join(', '))}">[来源引用]</span>`;
    }
    if (name === 'icon') return `<span class="dil-icon" aria-hidden="true">${esc(({ 'arrow-down': '↓', 'arrow-right': '→', 'arrow-down-up': '↕', check: '✓', star: '★', zap: 'ϟ' } as Record<string, string>)[text(a.name)] ?? '◇')}</span>`;
    if (name === 'divider' || name === 'separator') return '<hr>';
    const content = children(node.children, context, true, depth);
    const inner = content.replace(/^<p>([\s\S]*)<\/p>\n?$/, '$1');
    if (name === 'link') { const url = safeRemoteURL(a.url ?? a.href); return url ? `<a href="${esc(url)}">${inner || esc(a.title ?? url)}</a>` : inner || esc(a.title); }
    if (name === 'bold' || name === 'italic' || name === 'badge') { const tag = name === 'bold' ? 'strong' : name === 'italic' ? 'em' : 'span'; return `<${tag} class="dil-${name}">${inner}</${tag}>`; }
    if (name === 'title') return `<h3 class="dil-title">${inner}</h3>`;
    if (name === 'text' || name === 'caption') return `<div class="dil-${name}">${content}</div>`;
    if (name === 'table') return `<div class="dil-table-wrap"><table>${content}</table></div>`;
    const table: Record<string, string> = { 'table-head': 'thead', 'table-body': 'tbody', 'table-row': 'tr', 'table-cell': a.header ? 'th' : 'td', list: 'ul', 'list-item': 'li' };
    if (table[name]) return `<${table[name]}>${content}</${table[name]}>`;
    if (name === 'button' || name === 'radio' || name === 'checkbox' || name === 'input' || name === 'slider' || name === 'select' || name === 'progress') {
      limited = true;
      return `<div class="dil-static-control">${content || esc(a.label ?? a.placeholder ?? name)}${a.value !== undefined ? ` <span>${esc(a.value)}</span>` : ''}<small>存档快照</small></div>`;
    }
    if (name === 'appblock' || singles.has(name)) {
      limited = true; return `<div class="dil-static-control">${esc(a.title ?? name)}${content}<small>请在原始内容中查看完整组件</small></div>`;
    }
    const classes = ['dil-layout', 'dil-' + name];
    if (a.border) classes.push('dil-bordered');
    if (a.background) classes.push('dil-surface');
    if (a.flex) classes.push('dil-flex');
    if (name === 'grid') classes.push('dil-cols-' + Math.max(1, Math.min(4, Number(a.columns) || 2)));
    if (a.padding) classes.push('dil-padded');
    return `<div class="${classes.join(' ')}">${content}</div>`;
  }
  const rendered = children(tree, scope, false);
  const note = limited ? '<div class="dil-note">部分交互或表达式以存档快照展示。</div>' : '';
  return sanitize(`<div class="dil-response">${rendered}</div>${note}<details class="dil-source"><summary>查看原始内容</summary><pre><code>${esc(body || dil?.fallbackMarkdown || '')}</code></pre></details>`);
}

export function decorateMessageImages(root: ParentNode): void {
  root.querySelectorAll<HTMLButtonElement>('.dil-load-image').forEach(button => {
    button.onclick = () => {
      const url = safeRemoteURL(button.dataset.imageUrl);
      if (!url) return;
      const image = document.createElement('img');
      image.alt = button.dataset.imageAlt || '回答中的图片'; image.loading = 'lazy'; image.referrerPolicy = 'no-referrer';
      image.onload = () => button.remove();
      image.onerror = () => { image.remove(); button.disabled = false; button.textContent = '图片加载失败，点击重试'; };
      button.disabled = true; button.textContent = '正在加载…'; button.parentElement!.append(image); image.src = url;
    };
  });
}
