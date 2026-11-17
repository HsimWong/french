import { api, esc, pageHead, state, toast } from './ui-core.js';
import { deleteNote, reloadChapterNotes, saveNote } from "./notes.js";

// Dictionary lookup and the combined note/dictionary drawer.
// Notes stay on the existing /api/notes endpoints and keep their text offsets;
// The reader is deliberately unannotated; the drawer only opens on selection.

const drawer = document.getElementById('dict-drawer');
const drawerQuery = document.getElementById('dict-query');
const drawerResult = document.getElementById('dict-result');
const drawerSource = document.getElementById('dict-source');
const drawerQuote = document.getElementById('drawer-quote');
const drawerText = document.getElementById('drawer-text');
const drawerSave = document.getElementById('drawer-save');
const drawerDelete = document.getElementById('drawer-delete');
const drawerClose = document.getElementById('dict-close');

const lookupCache = new Map();
let lookupSeq = 0;

// Shared, dependency-free page helpers (escaping, headings, toasts).

/* ---------- lookup and rendering ---------- */
async function dictLookup(q, { fresh = false } = {}) {
  q = String(q || '').trim();
  if (!q) return null;
  if (!fresh && lookupCache.has(q)) return lookupCache.get(q);
  const d = await api('/api/dict/lookup?q=' + encodeURIComponent(q));
  lookupCache.set(q, d);
  if (lookupCache.size > 200) lookupCache.delete(lookupCache.keys().next().value);
  return d;
}

function formsHTML(m, current) {
  if (!m.forms || !m.forms.length) return '';
  // Standalone forms only: the course reader shows a compact paradigm.
  const plain = m.forms.filter(f => !/\s/.test(f.form) || /[’'-]/.test(f.form));
  if (!plain.length) return '';
  const currentNorm = String(current || '').toLowerCase();
  const chips = plain.slice(0, 60).map(f => {
    const cur = f.form === currentNorm ? ' current' : '';
    return '<span class="dict-form-chip' + cur + '" title="' + esc(f.feat || '') + '">' + esc(f.form) + '</span>';
  }).join('');
  return '<div class="dict-forms">' + chips + '</div>';
}

function entryHTML(m, current) {
  let html = '<div class="dict-entry">';
  html += '<div class="dict-lemma">' + esc(m.display || m.lemma) +
    '<span class="dict-pos">' + esc(m.pos || '') + '</span></div>';
  const zh = m.gloss_zh || '';
  // Never dress an English gloss up as Chinese: label it explicitly.
  if (zh) html += '<div class="dict-gloss">' + esc(zh) + '</div>';
  if (m.gloss_en && m.gloss_en !== zh) {
    html += '<div class="' + (zh ? 'dict-en' : 'dict-gloss') + '">' +
      (zh ? '' : '<span class="dict-untl">待译 · </span>') + esc(m.gloss_en) + '</div>';
  }
  if (m.ipa) html += '<div class="dict-en">/' + esc(m.ipa) + '/</div>';
  if (!zh && !m.gloss_en) html += '<div class="dict-gloss">暂无释义，仅记录拼写与词性。</div>';
  html += formsHTML(m, current) + '</div>';
  return html;
}

function renderLookup(d, q) {
  if (!d) return '<div class="dict-empty">没有输入查询词。</div>';
  if (d.source === 'none' || !d.matches || !d.matches.length) {
    const suggestions = (d.suggestions || []).map(s =>
      '<button type="button" class="dict-suggestion" data-dict-query="' + esc(s.display || s.lemma) + '">' +
      '<strong>' + esc(s.display || s.lemma) + '</strong> <span class="dict-pos">' + esc(s.pos || '') + '</span> ' +
      '<span class="dict-gloss">' + esc(s.gloss_zh || '') + '</span></button>').join('');
    let html = '<div class="dict-empty"><div>没有找到 <strong>' + esc(q) + '</strong> 的词典记录。</div>' +
      '<div style="margin-top:4px">可能是专名、拼写变体，或尚未收录；已加入待确认词形。</div></div>';
    if (suggestions) html += '<div style="margin-top:8px;font-size:11px;color:var(--faint)">相近词条</div>' + suggestions;
    return html;
  }
  const warning = d.source === 'rule'
    ? '<div class="dict-empty" style="font-size:12px;margin-bottom:5px">按词尾规则推测的原形，仅供参考。</div>' : '';
  return warning + d.matches.map(m => entryHTML(m, d.normalized || q)).join('');
}

function drawerShow() {
  if (!drawer) return;
  drawer.classList.remove('hidden');
  document.body.classList.add('dict-open');
}

function drawerHide() {
  if (!drawer) return;
  drawer.classList.add('hidden');
  document.body.classList.remove('dict-open');
  window.getSelection()?.removeAllRanges();
}

function setDrawerSource(text, kind = '') {
  drawerSource.textContent = text;
  drawerSource.className = 'dict-badge ' + kind;
}

async function showDictCard(q) {
  drawerShow();
  drawerQuery.textContent = q;
  setDrawerSource('查询中…');
  drawerResult.innerHTML = '<div class="dict-empty">正在查词…</div>';
  const seq = ++lookupSeq;
  try {
    const d = await dictLookup(q);
    if (seq !== lookupSeq) return null;
    drawerResult.innerHTML = renderLookup(d, q);
    const hit = d && d.matches && d.matches.length;
    setDrawerSource(hit ? (d.source === 'rule' ? '规则推测' : '词形反查') : '未收录', hit ? 'ok' : 'warn');
    return d;
  } catch (err) {
    if (seq === lookupSeq) {
      drawerResult.innerHTML = `<div class="dict-empty">词典暂时不可用：${esc(err.message)}<br>笔记仍可正常保存。</div>`;
      setDrawerSource('词典失败', 'warn');
    }
    return null;
  }
}

function setNoteMode(mode) {
  document.getElementById('note-state').textContent = mode === 'editing' ? '编辑中' : mode === 'saved' ? '已保存' : '未保存';
  document.getElementById('note-state').classList.toggle('saved', mode === 'saved');
}

/* The drawer serves both the text-selection draft and an existing note. */
function openDrawer({ quote, draft = null, note = null, query, findExisting = false }) {
  const q = String(query || quote || '').trim();
  drawerQuote.textContent = quote || note?.quote || q;
  let existing = note;
  if (!existing && draft && findExisting) {
    existing = (state.chapter?.notes || []).find(n =>
      n.block_id === draft.block_id && n.start_offset === draft.start_offset && n.end_offset === draft.end_offset) || null;
  }
  state.editingNoteId = existing?.id ?? null;
  drawerText.value = existing?.text || '';
  drawerDelete.classList.toggle('hidden', !existing);
  drawerSave.textContent = existing ? '更新笔记' : '保存笔记';
  setNoteMode(existing ? 'saved' : 'idle');
  drawerQuote.classList.toggle('hidden', !(quote || note?.quote));
  state.pendingSelection = draft;
  return showDictCard(q);
}

function dictQueryForSelection(text) {
  const words = String(text || '').trim().split(/\s+/);
  if (words.length === 1 || words.length === 2) return String(text).trim();
  if (words.length <= 4) return words.join(' ');
  return wordPatternJS(text).find(w => /[A-Za-zÀ-ÿ]/.test(w)) || '';
}

function wordPatternJS(text) {
  return String(text || '').match(/[A-Za-zÀ-ÿŒœÆæ][A-Za-zÀ-ÿŒœÆæ’'\-]*/g) || [];
}

let noteBusy = false;
drawerSave.addEventListener('click', async () => {
  if (noteBusy) return;
  const text = drawerText.value.trim();
  if (!text) { toast('请先写下笔记内容'); return; }
  const noteId = state.editingNoteId || null;
  let draft = state.pendingSelection;
  if (!draft && noteId) {
    // Reopened from an existing highlight: reuse the note's stored range.
    const n = (state.chapter?.notes || []).find(x => x.id === noteId);
    if (n) {
      draft = {
        chapter_id: state.chapter.chapter.id, block_id: n.block_id,
        page_no: n.page_no, start_offset: n.start_offset, end_offset: n.end_offset, quote: n.quote,
      };
    }
  }
  if (!noteId && !draft) { toast('请重新选中原文后再保存笔记'); return; }
  noteBusy = true;
  drawerSave.disabled = true;
  setNoteMode('editing');
  try {
    // Write exactly once. The notes module then pulls the chapter and rebuilds
    // every highlighted block from its original text.
    if (noteId) {
      await api(`/api/notes/${noteId}`, { method: 'PUT', body: JSON.stringify({ text }) });
    } else {
      await api('/api/notes', { method: 'POST', body: JSON.stringify({ ...draft, text }) });
    }
    const notes = await reloadChapterNotes();
    const current = (notes || []).find(n => (noteId ? n.id === noteId
      : draft && n.block_id === draft.block_id && n.start_offset === draft.start_offset));
    if (current) {
      state.editingNoteId = current.id;
      state.pendingSelection = {
        chapter_id: current.chapter_id || state.chapter.chapter.id,
        block_id: current.block_id, page_no: current.page_no,
        start_offset: current.start_offset, end_offset: current.end_offset, quote: current.quote,
      };
      drawerDelete.classList.remove('hidden');
      drawerSave.textContent = '更新笔记';
      setNoteMode('saved');
    }
    toast(noteId ? '笔记已更新' : '笔记已保存，原文保持高亮');
  } catch (err) {
    setNoteMode('idle');
    toast('保存失败：' + err.message);
  } finally {
    noteBusy = false;
    drawerSave.disabled = false;
  }
});

drawerDelete.addEventListener('click', async () => {
  if (!state.editingNoteId || noteBusy) return;
  if (!confirm('删除这条笔记？原文高亮也会随之取消。')) return;
  noteBusy = true;
  try {
    await deleteNote(state.editingNoteId);
    state.editingNoteId = null;
    drawerText.value = '';
    drawerDelete.classList.add('hidden');
    drawerSave.textContent = '保存笔记';
    setNoteMode('idle');
    await reloadChapterNotes();
    toast('笔记已删除');
  } catch (err) { toast('删除失败：' + err.message); }
  finally { noteBusy = false; }
});

drawerClose.addEventListener('click', drawerHide);
drawer.addEventListener('click', e => {
  const btn = e.target.closest('[data-dict-query]');
  if (btn) {
    const q = btn.dataset.dictQuery;
    openDrawer({ quote: q, query: q });
  }
});

/* URL-based word lookup, used by the dictionary page and quick jumps. */
document.addEventListener('fp:dict', e => {
  const q = e.detail?.q;
  if (q && state.insideReader) openDrawer({ quote: q, query: q });
});

/* ---------- static dictionary browser (#/dictionary) ---------- */
export async function viewDictionary(params) {
  state.insideReader = false;
  const q = params.get('q') || '';
  const courseId = Number(params.get('course') || state.courseId);
  // Courses are loaded once by the app shell, then shared through the
  // session state; refetch only when the shell has not populated it yet.
  let all = state.courses;
  if (!all) {
    all = (await (await fetch('/api/courses')).json()).courses || [];
    state.courses = all;
  }
  const course = all.find(c => c.id === courseId) || all[0];
  let suggestions = [];
  if (q) {
    suggestions = (await api('/api/dict/suggest?q=' + encodeURIComponent(q) + '&limit=30')).suggestions || [];
  }
  let lookup = null;
  if (q.trim()) lookup = await dictLookup(q.trim()).catch(() => null);
  const rows = suggestionRows(lookup, suggestions);
  document.getElementById('app').innerHTML = `
    ${pageHead('词典', '输入原形、变位或短语；支持“suis”这类同形词的全部候选')}
    <form class="dict-search" id="dict-search-form" autocomplete="off">
      <input id="dict-search-input" type="search" placeholder="查法语：être, suis, aller…" value="${esc(q)}">
      <div id="dict-suggestions" class="dict-suggestions hidden"></div>
    </form>
    <div class="card">
      <div class="dict-beta">离线词典 · ${rows.length ? rows.length + ' 个结果' : '输入词形开始查词'}${lookup?.source === 'rule' ? ' · 规则推测' : ''}</div>
      ${lookup ? renderLookup(lookup, q) : '<div class="dict-empty">输入一个词，或从下方最近查询开始。</div>'}
    </div>
    <div class="card" style="margin-top:16px">
      <div class="dict-list-head">
        <div class="section-label" style="margin-top:0">全部词条</div>
        <div class="dict-letters" id="dict-letters"></div>
      </div>
      <div id="dict-word-status" class="dict-beta"></div>
      <div id="dict-chapter-list"><div class="dict-empty">正在读取…</div></div>
      <div class="dict-pager" id="dict-pager"></div>
    </div>`;

  const input = document.getElementById('dict-search-input');
  const box = document.getElementById('dict-suggestions');
  let timer;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    const v = input.value.trim();
    if (!v) { box.classList.add('hidden'); return; }
    timer = setTimeout(async () => {
      try {
        const s = (await api('/api/dict/suggest?q=' + encodeURIComponent(v))).suggestions || [];
        box.innerHTML = s.map(x => `<button type="button" class="dict-suggestion" data-dict-pick="${esc(x.lemma)}">
          <strong>${esc(x.display)}</strong> <span class="dict-pos">${esc(x.pos)}</span>
          <span class="dict-gloss">${esc(x.gloss_zh)}</span></button>`).join('') || '<div class="dict-empty" style="padding:10px 14px">没有前缀匹配，回车直接查询。</div>';
        box.classList.remove('hidden');
      } catch (_) { box.classList.add('hidden'); }
    }, 180);
  });
  box.addEventListener('click', e => {
    const b = e.target.closest('[data-dict-pick]');
    if (b) { input.value = b.dataset.dictPick; box.classList.add('hidden'); input.form.requestSubmit(); }
  });
  document.getElementById('dict-search-form').addEventListener('submit', e => {
    e.preventDefault();
    const v = input.value.trim();
    box.classList.add('hidden');
    location.hash = `#/dictionary?q=${encodeURIComponent(v)}&course=${course.id}`;
  });

  /* Alphabetical browser. Every entry is reachable through the pager, so
     nothing silently drops out after the first few hundred words. */
  // Only letters present in the dictionary are selectable; the full dump no
  // longer contributes digits, braille or emoji to this alphabet bar.
  let letters = [];
  try {
    const s = await api('/api/dict/stats');
    if (s && s.letters) letters = String(s.letters).split('');
  } catch (_) {}
  document.getElementById('dict-letters').innerHTML =
    `<button type="button" class="dict-letter" data-letter="">全部</button>` +
    letters.map(ch => `<button type="button" class="dict-letter" data-letter="${esc(ch)}">${esc(ch)}</button>`).join('');
  const letter = (params.get('letter') || '').toUpperCase().slice(0, 1);
  const pos = params.get('pos') || '';
  const list = document.getElementById('dict-chapter-list');
  const pager = document.getElementById('dict-pager');
  const status = document.getElementById('dict-word-status');
  const browse = async (page = 1, l = letter, p = pos) => {
    list.innerHTML = '<div class="dict-empty">正在读取词条…</div>';
    pager.innerHTML = '';
    try {
      const d = await api(`/api/dict/words?per=60&page=${page}&letter=${encodeURIComponent(l)}&pos=${encodeURIComponent(p)}`);
      status.textContent = `共 ${d.total} 个词条 · 第 ${d.page}/${d.pages || 1} 页`;
      list.innerHTML = (d.words || []).length
        ? d.words.map(w => `<a class="dict-word-row" href="#/dictionary?q=${encodeURIComponent(w.lemma)}&course=${course.id}&letter=${l}&pos=${p}&page=${page}">
            <span class="lemma">${esc(w.display || w.lemma)}</span>
            <span class="pos dict-pos">${esc(w.pos || '')}</span>
            <span class="gloss">${esc(w.gloss_zh || '')}</span></a>`).join('')
        : '<div class="dict-empty">这个字母下没有词条。</div>';
      pager.innerHTML = d.pages > 1
        ? `<button type="button" class="btn btn-sm" data-page="${page - 1}" ${page <= 1 ? 'disabled' : ''}>上一页</button>
           <span class="dict-beta" style="margin:0">${page} / ${d.pages}</span>
           <button type="button" class="btn btn-sm" data-page="${page + 1}" ${page >= d.pages ? 'disabled' : ''}>下一页</button>`
        : '';
    } catch (err) {
      list.innerHTML = `<div class="dict-empty">词条读取失败：${esc(err.message)}</div>`;
    }
  };
  const pageNum = Number(params.get('page')) || 1;
  const initialLetter = q ? '' : letter;
  await browse(pageNum, initialLetter, pos);
  document.getElementById('dict-letters').addEventListener('click', e => {
    const b = e.target.closest('[data-letter]');
    if (!b) return;
    browse(1, b.dataset.letter, pos);
    document.querySelectorAll('.dict-letter').forEach(x => x.classList.toggle('active', x === b));
  });
  pager.addEventListener('click', e => {
    const b = e.target.closest('[data-page]');
    if (b && !b.disabled) browse(Number(b.dataset.page), letter, pos);
  });
}

function suggestionRows(lookup, suggestions) {
  if (lookup?.matches?.length) return lookup.matches;
  return suggestions;
}

// Reuses a lookup already cached in this session (the app shell warms the
// cache from every chapter's glossary).
export function cacheLookup(q, data) {
  if (q && data) lookupCache.set(String(q).trim(), data);
}

function showSelectionDrawer(rect, draft) {
  const q = dictQueryForSelection(draft.quote);
  void rect;
  return openDrawer({ quote: draft.quote, draft, query: q || draft.quote, findExisting: true });
}

export {
  openDrawer, drawerHide, showSelectionDrawer, dictLookup,
};
