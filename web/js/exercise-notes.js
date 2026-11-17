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
  const d = await api(`/api/notes?chapter_id=${chapterID}`);
  const stems = (d.notes || []).filter(n => n.block_id && Number(n.block_id) >= EX_OFFSET);
  state.stemNotes = stems;
  return stems;
}

// A selection inside an exercise statement becomes a normal note draft.
export function draftFromSelection(blockEl, range, extra) {
  const pre = document.createRange();
  pre.selectNodeContents(blockEl);
  try {
    pre.setEnd(range.startContainer, range.startOffset);
  } catch (_) {
    return null;
  }
  const start = pre.toString().length;
  const end = start + range.toString().length;
  if (end <= start) return null;
  return {
    chapter_id: extra.chapter_id,
    block_id: exerciseStemKey(extra.exercise_id, extra.question_id),
    page_no: extra.question_id,
    start_offset: start,
    end_offset: end,
    quote: range.toString(),
  };
}

// Wrap an exercise note after render. A widget is never wrapped; text between
// two blanks is walked as text nodes and marked with the shared mark.hl style.
export function paintStemNote(stemEl, note) {
  if (!stemEl || !note) return;
  const start = Number(note.start_offset), end = Number(note.end_offset);
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

export function stemsForBlock(blockID) {
  return (state.stemNotes || []).filter(n => Number(n.block_id) === Number(blockID));
}
