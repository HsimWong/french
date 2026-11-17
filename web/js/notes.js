import { api, state, ui } from './ui-core.js';

// Note persistence and the yellow highlights in the reading view.
// The dictionary drawer imports these directly, so updating a note never
// depends on helpers that live inside the page module.


export function noteBlock(bid) {
  const host = ui.app || document.querySelector('#app');
  return host ? host.querySelector(`[data-block-id="${bid}"]`) : null;
}

function textNodes(root) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const out = [];
  let n;
  while ((n = walker.nextNode())) out.push(n);
  return out;
}

function wrapRange(blockEl, start, end, noteId) {
  const before = blockEl.querySelector(`mark.hl[data-note-id="${noteId}"]`);
  if (before) {
    const same = before.textContent === blockEl.textContent.slice(start, end);
    before.replaceWith(...before.childNodes);
    if (same) return;
  }
  const targets = [];
  let pos = 0;
  for (const node of textNodes(blockEl)) {
    const ns = pos, ne = pos + node.nodeValue.length;
    pos = ne;
    if (node.parentElement?.closest('mark.hl')) continue;
    if (ne > start && ns < end) {
      targets.push({ node, from: Math.max(start, ns) - ns, to: Math.min(end, ne) - ns });
    }
    if (pos >= end) break;
  }
  for (let i = targets.length - 1; i >= 0; i--) {
    const { node, from, to } = targets[i];
    if (!node.parentNode) continue;
    const r = document.createRange();
    try {
      r.setStart(node, from);
      r.setEnd(node, to);
      const mark = document.createElement('mark');
      mark.className = 'hl';
      mark.dataset.noteId = noteId;
      r.surroundContents(mark);
    } catch (_) { /* a range crossing element boundaries is left unmarked */ }
  }
}

export function applyHighlights(notes) {
  const byBlock = {};
  (notes || []).forEach(n => {
    if (n.block_id) (byBlock[n.block_id] ||= []).push(n);
  });
  Object.entries(byBlock).forEach(([bid, list]) => {
    const blockEl = noteBlock(bid);
    const src = state.blocksById[bid];
    if (!blockEl || !src) return;
    blockEl.innerHTML = src.body_html;
    list.slice().sort((a, b) => b.start_offset - a.start_offset)
      .forEach(n => wrapRange(blockEl, n.start_offset, n.end_offset, n.id));
  });
}

export async function reloadChapterNotes() {
  if (!state.chapter?.chapter?.id) return;
  const id = state.chapter.chapter.id;
  const d = await api(`/api/chapters/${id}`);
  state.chapter.notes = d.notes || [];
  // The chapter is re-read while this request was in flight only if the user
  // navigated; in that case never paint highlights into a newer chapter.
  if (state.chapter.chapter.id !== id) return state.chapter.notes;
  // draw() re-renders every highlighted block from the untouched book HTML,
  // so an update can never leave two nested highlights for the same note.
  Object.keys(state.blocksById || {}).forEach(bid => {
    const el = noteBlock(bid);
    const src = state.blocksById[bid];
    if (el && src) el.innerHTML = src.body_html;
  });
  applyHighlights(state.chapter.notes);
  const link = document.querySelector('#app .toc-page a[href="#/notes"]');
  if (link) link.textContent = `笔记汇总(${state.chapter.notes.length})`;
  return state.chapter.notes;
}

export function selectionOffsets(blockEl, range) {
  const pre = document.createRange();
  pre.selectNodeContents(blockEl);
  try {
    pre.setEnd(range.startContainer, range.startOffset);
  } catch (_) {
    return null;
  }
  const start = pre.toString().length;
  return { start, end: start + range.toString().length };
}

export async function saveNote({ draft, noteId, text }) {
  const body = { text };
  if (noteId) {
    await api(`/api/notes/${noteId}`, { method: 'PUT', body: JSON.stringify(body) });
    return noteId;
  }
  if (!draft) throw new Error('请重新选中原文后再保存笔记');
  const r = await api('/api/notes', { method: 'POST', body: JSON.stringify({ ...draft, text }) });
  return r.id;
}

export async function deleteNote(noteId) {
  await api(`/api/notes/${noteId}`, { method: 'DELETE' });
}

// The old floating note box and the right-hand drawer must never both hold
// an editing session; clear it whenever the drawer takes over.
