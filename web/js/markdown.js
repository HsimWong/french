// Small, dependency-free Markdown subset for free-form chapter summaries.
// Everything is HTML-escaped before markup is applied, and links/images are
// restricted to safe targets, so a note can never inject HTML or a
// javascript: URL into the page.
import { esc } from './ui-core.js';

function inline(src) {
  let s = esc(src);
  // Images: only the app's own note-image endpoint is allowed.
  s = s.replace(/!\[([^\]]*)\]\((\/?api\/note-images\/\d+)\)/g,
    (_, alt, url) => `<img class="md-img" src="${url}" alt="${alt || '图片'}" loading="lazy">`);
  // Links: http(s) and same-page anchors only.
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|#[^\s)]+)\)/g,
    (_, text, url) => `<a href="${url}" target="_blank" rel="noopener noreferrer">${text}</a>`);
  s = s.replace(/`([^`\n]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
  return s;
}

const BLOCK_START = /^\s*(#{1,3}\s|>|```|[-*]\s|\d+\.\s)/;

export function renderMarkdown(text) {
  const lines = String(text || '').replace(/\r\n?/g, '\n').split('\n');
  let out = '', i = 0, list = null;
  const closeList = () => {
    if (list) { out += `</${list}>`; list = null; }
  };
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { closeList(); i++; continue; }
    if (/^\s*```/.test(line)) {
      closeList();
      const code = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) code.push(lines[i++]);
      if (i < lines.length) i++;
      out += `<pre class="md-pre"><code>${esc(code.join('\n'))}</code></pre>`;
      continue;
    }
    const h = line.match(/^\s*(#{1,3})\s+(.*)$/);
    if (h) {
      closeList();
      const lv = Math.min(5, h[1].length + 2); // page h1 stays unique
      out += `<h${lv} class="md-h">${inline(h[2])}</h${lv}>`;
      i++;
      continue;
    }
    const q = line.match(/^\s*>\s?(.*)$/);
    if (q) {
      closeList();
      out += `<blockquote class="md-quote">${inline(q[1])}</blockquote>`;
      i++;
      continue;
    }
    const ul = line.match(/^\s*[-*]\s+(.*)$/);
    const ol = line.match(/^\s*\d+\.\s+(.*)$/);
    if (ul || ol) {
      const want = ul ? 'ul' : 'ol';
      if (list !== want) { closeList(); out += `<${want} class="md-list">`; list = want; }
      out += `<li>${inline((ul || ol)[1])}</li>`;
      i++;
      continue;
    }
    closeList();
    const para = [line];
    i++;
    while (i < lines.length && lines[i].trim() && !BLOCK_START.test(lines[i])) para.push(lines[i++]);
    out += `<p class="md-p">${inline(para.join('\n')).replace(/\n/g, '<br>')}</p>`;
  }
  closeList();
  return out;
}

// Plain-text excerpt for compact lists; image references become "[图]".
export function markdownExcerpt(text, limit = 180) {
  const plain = String(text || '')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '[图]')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/[#>*`_~]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return plain.length > limit ? plain.slice(0, limit) + '…' : plain;
}
