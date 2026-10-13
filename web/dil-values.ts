// A bounded reader for DIL's static values. It never calls model-written code.
export type Scope = Record<string, unknown>;
const forbidden = new Set(['__proto__', 'prototype', 'constructor']);

export function staticValue(source: string, scope: Scope): unknown {
  source = source.trim();
  if (source.startsWith('`') && source.endsWith('`')) {
    let valid = true;
    const value = source.slice(1, -1).replace(/\$\{([^{}]+)\}/g, (_, expression: string) => {
      const result = staticValue(expression, scope);
      if (typeof result !== 'string' && typeof result !== 'number') { valid = false; return ''; }
      return String(result);
    });
    return valid && !value.includes('${') ? value : undefined;
  }
  const tokens = source.match(/\s+|(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|(?:\d+(?:\.\d+)?)|(?:[A-Za-z_$][\w$]*)|(?:===|!==|==|!=|>=|<=|&&|\|\||\?\?)|./g)?.filter(t => !/^\s+$/.test(t)) || [];
  if (tokens.length > 2000) return undefined;
  let i = 0, depth = 0;
  const precedence: Record<string, number> = { '??': 1, '||': 1, '&&': 2, '===': 3, '!==': 3, '==': 3, '!=': 3, '>': 4, '<': 4, '>=': 4, '<=': 4, '+': 5, '-': 5, '*': 6, '/': 6, '%': 6 };
  const take = (token: string) => { if (tokens[i] !== token) throw new Error('Incomplete value'); i++; };
  const key = () => {
    const token = tokens[i++];
    if (!token) throw new Error('Missing key');
    const value = /^['"]/.test(token) ? string(token) : token;
    if (forbidden.has(value)) throw new Error('Invalid key');
    return value;
  };
  function string(token: string): string {
    if (token[0] === '"') return JSON.parse(token);
    return token.slice(1, -1).replace(/\\(['\\nrt])/g, (_, c: string) => ({ n: '\n', r: '\r', t: '\t' }[c] ?? c));
  }
  function atom(): unknown {
    if (++depth > 40) throw new Error('Value too deep');
    const token = tokens[i++];
    let result: unknown;
    if (token === '(') { result = expression(); take(')'); }
    else if (token === '[') {
      const values: unknown[] = [];
      while (i < tokens.length && tokens[i] !== ']') { values.push(expression()); if (tokens[i] !== ',') break; i++; }
      take(']'); result = values;
    } else if (token === '{') {
      const values = Object.create(null) as Scope;
      while (i < tokens.length && tokens[i] !== '}') { const name = key(); take(':'); values[name] = expression(); if (tokens[i] !== ',') break; i++; }
      take('}'); result = values;
    } else if (token === '!' || token === '-' || token === '+') {
      const value = atom(); result = token === '!' ? !value : token === '-' ? -Number(value) : Number(value);
    } else if (/^['"]/.test(token || '')) result = string(token);
    else if (/^\d/.test(token || '')) result = Number(token);
    else if (token === 'true' || token === 'false') result = token === 'true';
    else if (token === 'null') result = null;
    else if (token && !forbidden.has(token) && Object.hasOwn(scope, token)) result = scope[token];
    else throw new Error('Unknown value');
    while (tokens[i] === '.' || tokens[i] === '[') {
      let property: string;
      if (tokens[i++] === '.') property = key();
      else { property = String(expression()); take(']'); }
      if (forbidden.has(property)) throw new Error('Invalid property');
      if (Array.isArray(result) && property === 'length') result = result.length;
      else if (result !== null && typeof result === 'object' && Object.hasOwn(result, property)) result = (result as Scope)[property];
      else result = undefined;
    }
    depth--; return result;
  }
  function expression(min = 0): unknown {
    let left = atom();
    while (i < tokens.length && (precedence[tokens[i]] ?? -1) >= min) {
      const op = tokens[i++], right = expression(precedence[op] + 1);
      switch (op) {
        case '+': left = typeof left === 'string' || typeof right === 'string' ? String(left) + String(right) : Number(left) + Number(right); break;
        case '-': left = Number(left) - Number(right); break;
        case '*': left = Number(left) * Number(right); break;
        case '/': left = Number(left) / Number(right); break;
        case '%': left = Number(left) % Number(right); break;
        case '==': case '===': left = left === right; break;
        case '!=': case '!==': left = left !== right; break;
        case '>': left = Number(left) > Number(right); break;
        case '<': left = Number(left) < Number(right); break;
        case '>=': left = Number(left) >= Number(right); break;
        case '<=': left = Number(left) <= Number(right); break;
        case '&&': left = left && right; break;
        case '||': left = left || right; break;
        case '??': left = left ?? right; break;
      }
    }
    if (min === 0 && tokens[i] === '?') { i++; const yes = expression(); take(':'); const no = expression(); left = left ? yes : no; }
    return left;
  }
  try { const result = expression(); return i === tokens.length && !(typeof result === 'number' && !Number.isFinite(result)) ? result : undefined; }
  catch { return undefined; }
}
