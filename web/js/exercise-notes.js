// Notes on exercise text. Reading-view notes are anchored to content_block
// offsets; question stems are HTML with live input widgets, so a note here is
// anchored to the blank's position in the stem instead. The same /api/notes row
// is used; the exercise id is encoded in block_id and the blank index in
// start_offset, leaving the original note table and its APIs intact.

import { api, state } from './ui-core.js';

const EX_OFFSET = 100000000;

export function exerciseStemKey(exerciseID, questionID) {
  // question ids have no natural upper bound, so the exercise lives in the
  // reminder: key = 100000001 + exercise*100000 + (question % 100000).
  return EX_OFFSET + exerciseID * 100000 + (questionID % 100000);
}

export function stemExercise(key) {
  return Math.floor((Number(key) - EX_OFFSET) / 100000);
}

export async function loadStemNotes(chapterID) {
  const d = await api(`api/notes?chapter_id=${chapterID}`);
  const stems = (d.notes || []).filter(n => n.question_id && Number(n.question_id) > 0);
  state.stemNotes = stems;
  return stems;
}

// The noteable text of a stem is the concatenation of its text nodes with the
// widgets left out. Offsets must use exactly this view of the DOM: a Range's
// toString() also contains button labels ("□", option text), which would shift
// every offset after the first blank.
function noteTextMap(root) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes = [];
  let text = '';
  let node;
  while ((node = walker.nextNode())) {
    // Existing highlights still occupy characters in the anchor space; only
    // widgets are excluded. Skipping mark.hl here would shift every new note
    // that starts after an earlier highlight.
    if (node.parentElement?.closest('input, textarea, button, .opt-group')) continue;
    nodes.push({ node, start: text.length, end: text.length + node.nodeValue.length });
    text += node.nodeValue;
  }
  return { nodes, text };
}

function boundaryOffset(container, offset, nodes) {
  if (container.nodeType === 3) {
    const rec = nodes.find(r => r.node === container);
    return rec ? rec.start + Math.max(0, Math.min(offset, rec.node.nodeValue.length)) : null;
  }
  if (container.nodeType === 1) {
    const child = container.childNodes[offset];
    if (!child) {
      let last = 0;
      for (const rec of nodes) if (container.contains(rec.node)) last = rec.end;
      return last;
    }
    const rec = nodes.find(r => r.node === child || child.contains?.(r.node));
    return rec ? rec.start : null;
  }
  return null;
}

export function stemSelectionOffsets(blockEl, range) {
  const { nodes, text } = noteTextMap(blockEl);
  const start = boundaryOffset(range.startContainer, range.startOffset, nodes);
  const end = boundaryOffset(range.endContainer, range.endOffset, nodes);
  if (start == null || end == null || end <= start) return null;
  return { start, end, text: text.slice(start, end) };
}

// A selection inside an exercise statement becomes a normal note draft.
export function draftFromSelection(blockEl, range, extra) {
  const off = stemSelectionOffsets(blockEl, range);
  if (!off) return null;
  const quote = off.text || range.toString();
  if (!quote.trim()) return null;
  return {
    chapter_id: Number(extra.chapter_id),
    // block_id is reserved for content_block rows (foreign key); exercise
    // anchors use their own columns.
    block_id: null,
    exercise_id: Number(extra.exercise_id),
    question_id: Number(extra.question_id),
    page_no: null,
    start_offset: off.start,
    end_offset: off.end,
    quote,
  };
}

// Wrap an exercise note after render. A widget is never wrapped; text between
// two blanks is walked as text nodes and marked with the shared mark.hl style.
export function paintStemNote(stemEl, note) {
  if (!stemEl || !note) return;
  const start = Number(note.start_offset), end = Number(note.end_offset);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return;
  const walker = document.createTreeWalker(stemEl, NodeFilter.SHOW_TEXT);
  const targets = [];
  let pos = 0, node;
  while ((node = walker.nextNode())) {
    const ns = pos, ne = pos + node.nodeValue.length;
    pos = ne;
    if (node.parentElement?.closest('mark.hl, input, textarea, button, .opt-group')) continue;
    if (ne > start && ns < end) targets.push({ node, from: Math.max(0, start - ns), to: Math.min(node.nodeValue.length, end - ns) });
  }
  for (let i = targets.length - 1; i >= 0; i--) {
    const { node, from, to } = targets[i];
    if (!node.parentNode || to <= from) continue;
    try {
      const r = document.createRange();
      r.setStart(node, from);
      r.setEnd(node, to);
      const m = document.createElement('mark');
      m.className = 'hl';
      m.dataset.noteId = note.id;
      r.surroundContents(m);
    } catch (_) { /* boundaries spanning elements are skipped */ }
  }
}

// Repaint every stem note without rebuilding the question markup: inputs keep
// whatever the learner has typed. Existing marks are unwrapped first so an
// edited or deleted note cannot leave a stale highlight behind.
export function paintStemNotes(root, notes) {
  const host = root || document.querySelector('#app');
  if (!host) return;
  const byKey = new Map();
  (notes || []).forEach(n => {
    if (!n.exercise_id || !n.question_id) return;
    const key = `${n.exercise_id}:${n.question_id}`;
    if (!byKey.has(key)) byKey.set(key, []);
    byKey.get(key).push(n);
  });
  for (const [key, list] of byKey) {
    const [exID, qID] = key.split(':');
    const el = host.querySelector(`.stem-text[data-exercise="${exID}"][data-question="${qID}"]`);
    if (!el) continue;
    el.querySelectorAll('mark.hl').forEach(m => m.replaceWith(...m.childNodes));
    el.normalize();
    list.slice().sort((a, b) => b.start_offset - a.start_offset)
      .forEach(n => paintStemNote(el, n));
  }
}

export function stemsForBlock(blockID) {
  return (state.stemNotes || []).filter(n => Number(n.block_id) === Number(blockID));
}
