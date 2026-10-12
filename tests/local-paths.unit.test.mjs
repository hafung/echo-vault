import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
import { Marked } from 'marked';

const source = await readFile(new URL('../web/local-paths.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } });
const { localPathTarget, barePathPattern } = await import('data:text/javascript;base64,' + Buffer.from(outputText).toString('base64'));
const cwd = 'D:/projects/demo';
const path = 'D:/projects/codex/2026-10-06/three-js-imagegen-skill-3d-tts/outputs/pip-and-the-little-star/dist/illustrations/prompts-journey.md';

test('drive and loopback file references recover the actual local destination', () => {
  for (const value of [path, path + ':12', `http://localhost:8765/${path}`, `http://127.0.0.1:8765/${path}#L12`, `http://[::1]:8765/${path}`, `file:///${path}`]) {
    assert.equal(localPathTarget(value, cwd, true), path, value);
  }
  assert.equal(localPathTarget('http://localhost:8765/D:/notes/%E4%B8%AD%E6%96%87%20file.md', cwd, true), 'D:/notes/中文 file.md');
  assert.equal(localPathTarget('D:\\projects\\folder', cwd), 'D:\\projects\\folder');
  assert.equal(localPathTarget('/home/zz/report.md:8:2', cwd), '/home/zz/report.md');
  assert.equal(localPathTarget('file:///home/zz/report.md', cwd, true), '/home/zz/report.md');
  assert.equal(localPathTarget('http://localhost:8765/home/zz/report.md', cwd, true), '/home/zz/report.md');
  assert.equal(localPathTarget('~/notes/file.md'), '~/notes/file.md');
});

test('relative file references require a project directory', () => {
  assert.equal(localPathTarget('outputs/notes.md', cwd, true), cwd + '/outputs/notes.md');
  assert.equal(localPathTarget('../notes.md', '/home/zz/demo', true), '/home/zz/demo/../notes.md');
  assert.equal(localPathTarget('./notes.md', '', true), null);
  assert.equal(localPathTarget('notes.md', cwd, true), cwd + '/notes.md');
  assert.equal(localPathTarget('ordinary words', cwd, true), null);
});

test('external and ordinary localhost URLs keep their web behavior', () => {
  for (const value of ['https://example.com/D:/notes/file.md', 'https://example.com/home/zz/file.md', 'http://localhost:3000/dashboard', 'http://localhost:8765', 'http://localhost.evil.example/D:/file.md', 'https://localhost@example.com/D:/file.md', 'mailto:a@example.com', '#section', 'javascript:alert(1)', '//example.com/file.md', 'file://server/share/file.md', 'D:/notes/%ZZ.md', 'D:/notes/a\x00.md']) {
    assert.equal(localPathTarget(value, cwd, true), null, value);
  }
});

test('Markdown keeps raw local destinations available before browser URL resolution', () => {
  const destinations = [];
  const parser = new Marked({ renderer: { link(token) {
    destinations.push([token.href, localPathTarget(token.href, cwd, true)]);
    return this.parser.parseInline(token.tokens);
  } } });
  parser.parse(`[file](${path}:12)\n\nhttp://localhost:8765/${path}\n\n[file URI](file:///D:/notes/file.md)\n\n[external](https://example.com/D:/notes/file.md)`);
  assert.deepEqual(destinations.map(([, target]) => target), [path, path, 'D:/notes/file.md', null]);
  assert.deepEqual([...'/home/zz/report.md D:/notes/file.md'.matchAll(barePathPattern)].map(match => match[0]), ['/home/zz/report.md', 'D:/notes/file.md']);
});
