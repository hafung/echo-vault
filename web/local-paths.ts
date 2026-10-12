const drivePath = /^[a-z]:[\\/]/i;
const localHost = /^(localhost|127\.0\.0\.1|\[?::1\]?)$/i;
const unixRoots = /^\/(home|Users|mnt|tmp|var|opt|usr|etc|root|Volumes)(\/|$)/;

/** Classify the original Markdown destination before the browser resolves it. */
export function localPathTarget(value: string, cwd = '', markdownLink = false): string | null {
  let path = value.trim();
  if (!path || /[\x00-\x1f]/.test(path)) return null;
  if (/^(https?:|file:)/i.test(path)) {
    let url: URL;
    try { url = new URL(path); } catch { return null; }
    if (url.protocol === 'file:') {
      if (url.hostname && !localHost.test(url.hostname)) return null;
    } else if (!localHost.test(url.hostname)) return null;
    try { path = decodeURIComponent(url.pathname); } catch { return null; }
    if (/^\/[a-z]:[\\/]/i.test(path)) path = path.slice(1);
    if (url.protocol !== 'file:' && !drivePath.test(path) && !unixRoots.test(path)) return null;
  } else if (markdownLink) {
    try { path = decodeURIComponent(path); } catch { return null; }
  }
  // Source references carry a line number, which is not part of the filename.
  path = path.replace(/#L?\d+(?:[-:]L?\d+)?$/i, '').replace(/:\d+(?::\d+)?$/, '');
  if (!path || /[\x00-\x1f]/.test(path)) return null;
  if (drivePath.test(path) || /^\/(?!\/)/.test(path) || /^~(?:\/|$)/.test(path)) return path;
  if (/^[a-z][a-z\d+.-]*:/i.test(path) || /^[#?]/.test(path) || path.startsWith('//')) return null;
  if (/^\.\.?[\\/]/.test(path) || (markdownLink && /[\\/]|\.[a-z\d]{1,12}$/i.test(path))) {
    if (!cwd || !(drivePath.test(cwd) || cwd.startsWith('/'))) return null;
    return cwd.replace(/[\\/]+$/, '') + '/' + path.replace(/\\/g, '/');
  }
  return null;
}

// Bare paths with spaces are handled when enclosed in inline code or a Markdown link.
export const barePathPattern = /(?:[a-z]:[\\/]|\/(?:home|Users|mnt|tmp|var|opt|usr|etc|root|Volumes)\/|~\/|\.\.?\/)[^\s<>"'`，。；！？]+/gi;
