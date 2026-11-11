const app = document.getElementById('app');
const popover = document.getElementById('note-popover');
const popoverQuote = document.getElementById('popover-quote');
const popoverText = document.getElementById('popover-text');
const popoverSave = document.getElementById('popover-save');
const popoverDelete = document.getElementById('popover-delete');
const popoverClose = document.getElementById('popover-close');
const toastEl = document.getElementById('toast');
const courseSwitch = document.getElementById('course-switch');

const state = {
  chapter: null,
  blocksById: {},
  pendingSelection: null,
  editingNoteId: null,
  noteQuery: '',
  noteCourse: '',
  courseId: Number(sessionStorage.getItem('fp.courseId') || 1),
  courses: null,
};

const FOLIO = {
  6: 'six', 7: 'sept', 8: 'huit', 9: 'neuf', 10: 'dix', 11: 'onze', 12: 'douze',
};

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

let toastTimer;
function toast(msg) {
  toastEl.textContent = msg;
  toastEl.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toastEl.classList.add('hidden'), 2600);
}

function parseHash() {
  const raw = location.hash.slice(1) || '/';
  const [path, qs] = raw.split('?');
  return { path: path || '/', params: new URLSearchParams(qs || '') };
}

async function getCourses() {
  return (await api('/api/courses')).courses;
}

function setCourse(id) {
  state.courseId = Number(id) || 1;
  sessionStorage.setItem('fp.courseId', String(state.courseId));
}

function courseShort(c) {
  if (!c) return '';
  return c.code.startsWith('vocabulaire')
    ? 'Vocabulaire progressif du français'
    : 'Grammaire progressive du français';
}

async function updateChrome(courseId) {
  const courses = await getCourses();
  const cur = courses.find(c => c.id === courseId) || courses[0];
  if (!cur) return;
  const brandTitle = document.querySelector('.brand-title');
  const brandSub = document.querySelector('.brand-sub');
  if (brandTitle) brandTitle.textContent = courseShort(cur);
  if (brandSub) brandSub.textContent = `${cur.level} · ${cur.code.startsWith('vocabulaire') ? '词汇课程' : '语法课程'}`;
  document.body.classList.toggle('vocab-course', cur.code.startsWith('vocabulaire'));
  if (courseSwitch) {
    if (!courseSwitch.options.length) {
      courses.forEach(c => {
        const o = document.createElement('option');
        o.value = String(c.id);
        o.textContent = c.code.startsWith('vocabulaire') ? '词汇 · Vocabulaire' : '语法 · Grammaire';
        courseSwitch.appendChild(o);
      });
    }
    courseSwitch.value = String(cur.id);
  }
  const first = cur.first_chapter_id;
  document.querySelectorAll('.nav a[data-route]').forEach(a => {
    const r = a.dataset.route;
    if (r === '/chapters') a.href = `#/chapters/${cur.id}`;
    if (r === '/chapter' && first) a.href = `#/chapter/${first}`;
    if (r === '/exercise' && first) a.href = `#/exercise/${first}`;
  });
}

function setNav(path) {
  document.querySelectorAll('.nav a[data-route]').forEach(a => {
    const r = a.dataset.route;
    let active = r === path;
    if (r === '/chapters') active = path.startsWith('/chapters');
    if (r === '/chapter') active = path.startsWith('/chapter/');
    if (r === '/exercise') active = path.startsWith('/exercise/');
    if (r === '/notes') active = path.startsWith('/notes');
    if (r === '/') active = path === '/' || path.startsWith('/course/');
    a.classList.toggle('active', active);
  });
}

async function refreshAnswerBadge() {
  try {
    const s = await api('/api/answers/status');
    const el = document.getElementById('answer-badge');
    if (s.ready > 0) {
      el.innerHTML = `<span class="badge ready">答案已导入 ${s.ready}/${s.total}</span>`;
    } else {
      el.innerHTML = `<span class="badge">答案待导入 · ${s.total} 题</span>`;
    }
  } catch (_) { /* ignore */ }
}

function pageHead(title, sub) {
  return `<div class="page-head"><h1>${esc(title)}</h1><p>${esc(sub)}</p></div>`;
}

function scanBlock(scanBase, pageNo) {
  if (!scanBase) return '';
  return `<div class="scan-wrap">
    <button type="button" class="btn btn-sm scan-toggle" data-scan="${pageNo}">对照原书 p. ${pageNo} ▾</button>
    <div class="scan-frame hidden" data-scan-frame="${pageNo}">
      <img src="${scanBase}${pageNo}.jpg" alt="原书印刷第 ${pageNo} 页影印" loading="lazy">
    </div>
  </div>`;
}

/* ------------------------------ course overview ------------------------------ */
async function viewHome() {
  const d = await getCourses();
  const totalChapters = d.reduce((n, c) => n + c.chapter_count, 0);
  const totalReady = d.reduce((n, c) => n + c.ready_count, 0);
  const totalNotes = d.reduce((n, c) => n + c.note_count, 0);
  const totalBlocks = d.reduce((n, c) => n + c.block_count, 0);
  app.innerHTML = `
    ${pageHead('学习台', 'Grammaire progressive · Vocabulaire progressif — 两门课程，按原书逐字录入')}
    <div class="metrics">
      <div class="metric"><div class="label">课程</div><div class="value">${d.length}</div><div class="hint">语法 + 词汇</div></div>
      <div class="metric"><div class="label">章节总数</div><div class="value">${totalChapters}</div><div class="hint">按原书目录 1:1 建章</div></div>
      <div class="metric"><div class="label">已录入内容</div><div class="value">${totalReady}</div><div class="hint">${totalBlocks} 个讲解块</div></div>
      <div class="metric"><div class="label">笔记</div><div class="value">${totalNotes}</div><div class="hint">高亮笔记总数</div></div>
    </div>
    <div class="section-label">选择课程</div>
    <div class="course-grid">
      ${d.map((c, i) => `
        <div class="course-card">
          <div class="course-index">${i === 0 ? 'GRAMMAIRE' : 'VOCABULAIRE'}</div>
          <h2>${esc(c.title_fr)}</h2>
          <div class="course-level">${esc(c.level)} · ${c.chapter_count} 章 · 已录入 ${c.ready_count} 章</div>
          <p class="course-src">${esc(c.source_book)}</p>
          <div class="course-stat">${c.block_count} 讲解块 · ${c.exercise_count} 个练习 · ${c.note_count} 条笔记</div>
          <div class="course-actions">
            <a class="btn btn-primary" href="#/course/${c.id}">进入课程</a>
            ${c.first_chapter_id ? `<a class="btn" href="#/chapter/${c.first_chapter_id}">第 1 章学习</a>` : ''}
            ${c.first_chapter_id ? `<a class="btn" href="#/exercise/${c.first_chapter_id}">在线练习</a>` : ''}
          </div>
        </div>`).join('')}
    </div>
    <div class="card" style="margin-top:18px">
      <div class="tip" style="border:0;margin:0;padding:0">
        章节学习页选中任意文字即可添加高亮笔记；词汇教材插图较多，每页都可展开「对照原书」影印页。
      </div>
    </div>`;
}

/* ------------------------------ one course home ------------------------------ */
async function viewCourse(courseId) {
  setCourse(courseId);
  const d = await api(`/api/course?course_id=${courseId}`);
  const courses = await getCourses();
  const cur = courses.find(c => c.id === Number(courseId));
  const ready = d.chapters.filter(c => c.status === 'ready');
  const first = d.chapters[0];
  const isVocab = d.course.code.startsWith('vocabulaire');
  app.innerHTML = `
    ${pageHead(d.course.title_fr, `${d.course.level} · ${d.course.source_book}`)}
    <div class="metrics">
      <div class="metric"><div class="label">课程章节</div><div class="value">${d.chapters.length}</div><div class="hint">按原书目录 1:1 建章</div></div>
      <div class="metric"><div class="label">已录入内容</div><div class="value">${ready.length}</div><div class="hint">第 1 章可学习可练习</div></div>
      <div class="metric"><div class="label">笔记</div><div class="value">${d.stats.notes}</div><div class="hint">本课程高亮笔记</div></div>
      <div class="metric"><div class="label">已交卷</div><div class="value">${d.stats.attempts}</div><div class="hint">${d.stats.questions} 道题目</div></div>
    </div>

    <div class="card">
      <div class="row" style="align-items:flex-start">
        <div style="flex:1;min-width:240px">
          <div style="font-size:12px;color:var(--muted)">CHAPITRE 1 · 印刷页 ${first.page_start}–${first.page_end}</div>
          <div style="font-size:20px;font-weight:600;color:var(--blue-dark);margin:6px 0 4px">${esc(first.title_fr)}</div>
          <div style="font-size:13px;color:var(--muted)">
            ${first.block_count} 个讲解块 · ${first.exercise_count} 个练习 · ${first.note_count} 条笔记
          </div>
          <div style="margin-top:16px;display:flex;gap:8px;flex-wrap:wrap">
            <a class="btn btn-primary" href="#/chapter/${first.id}">进入章节学习</a>
            <a class="btn" href="#/exercise/${first.id}">在线练习</a>
            <a class="btn" href="#/notes">查看笔记</a>
            <a class="btn" href="#/">切换课程</a>
          </div>
        </div>
        <div style="min-width:230px">
          <div class="section-label" style="margin-top:0">本教材</div>
          <div style="font-size:13px;line-height:1.9">
            <div>${esc(d.course.title_fr)}</div>
            <div>${esc(d.course.level)} · ${d.chapters.length} 章</div>
            <div>${isVocab ? '左页讲解 / 右页练习' : '讲解 + EXERCICES + Bilan'}</div>
          </div>
          <div class="tip" style="border:0;padding:0;margin-top:10px">
            ${isVocab ? '词汇教材的漫画与示意图是题目语境，每页可展开原书影印对照。' : '学习时选中任意文字即可添加笔记，笔记会高亮保存。'}
          </div>
        </div>
      </div>
    </div>

    <div class="section-label">章节目录（前 8 章）</div>
    <div class="chapter-grid">
      ${d.chapters.slice(0, 8).map(chapterCard).join('')}
    </div>
    <div style="margin-top:14px"><a class="btn" href="#/chapters/${courseId}">查看全部 ${d.chapters.length} 章</a></div>
    ${cur ? `<div style="margin-top:14px;font-size:12px;color:var(--faint)">${esc(cur.source_book)}</div>` : ''}
  `;
}

function chapterCard(c) {
  const locked = c.status !== 'ready';
  const inner = `
    <div class="ch-num">${c.seq}</div>
    <div style="min-width:0">
      <div class="ch-title">${esc(c.title_fr)}</div>
      <div class="ch-meta">p. ${c.page_start ?? '—'} · ${c.block_count} 讲解块 · ${c.exercise_count} 练习</div>
    </div>`;
  return locked
    ? `<div class="chapter-card locked">${inner}<span class="tag">待录入</span></div>`
    : `<a class="chapter-card" href="#/chapter/${c.id}" style="color:inherit">${inner}<span class="tag ready">可学习</span></a>`;
}

async function viewChapters(courseId) {
  setCourse(courseId);
  const d = await api(`/api/course?course_id=${courseId}`);
  app.innerHTML = `
    ${pageHead('课程目录', `${d.course.title_fr} — 原书 ${d.chapters.length} 章，标题与顺序一字未改`)}
    <div class="card" style="margin-bottom:16px;font-size:13px;color:var(--muted)">
      ${esc(d.course.source_book)}
    </div>
    <div class="chapter-grid">${d.chapters.map(chapterCard).join('')}</div>
  `;
}

/* --------------------------- chapter reading --------------------------- */
function blockHTML(b) {
  return `<div class="block ${esc(b.type)}" data-block-id="${b.id}" data-page-no="${b.page_no}">${b.body_html}</div>`;
}

function sheetHTML(pageNo, blocks, header, scanBase) {
  return `
    <section class="sheet" id="sheet-${pageNo}">
      <div class="sheet-head"><span>${esc(header)}</span><span class="folio">${pageNo} • ${FOLIO[pageNo] || ''}</span></div>
      ${scanBlock(scanBase, pageNo)}
      <div class="sheet-body">${blocks.map(blockHTML).join('')}</div>
    </section>`;
}

async function viewChapter(id, params) {
  const d = await api(`/api/chapters/${id}`);
  setCourse(d.chapter.course_id);
  if (d.chapter.status !== 'ready') {
    app.innerHTML = pageHead(d.chapter.title_fr, '本章内容尚未录入') +
      `<div class="card empty">按计划逐章录入，当前每门课程仅第 1 章可学习。</div>`;
    return;
  }
  state.chapter = d;
  state.blocksById = Object.fromEntries(d.blocks.map(b => [b.id, b]));

  const byPage = {};
  d.blocks.forEach(b => (byPage[b.page_no] ||= []).push(b));
  const pages = Object.keys(byPage).map(Number).sort((a, b) => a - b);

  app.innerHTML = `
    ${pageHead(d.chapter.title_fr, `${esc(d.chapter.course_title_fr)} · 印刷页 ${d.chapter.page_start}–${d.chapter.page_end} · PDF 页 ${d.chapter.pdf_start}–${d.chapter.pdf_end} · 选中文字可添加笔记`)}
    <div class="read-layout">
      <aside class="toc">
        <h3>本章目录</h3>
        ${d.sections.map((s, i) => `<a href="#sheet-${s.page_no}">${i + 1}. ${esc(s.title_fr)}<br><span style="color:var(--faint);font-size:11px">p. ${s.page_no}</span></a>`).join('')}
        <div class="toc-page">
          <a href="#exercises-jump">EXERCICES · ${d.exercise_count} 个大题</a>
          <a href="#/notes">笔记汇总(${d.notes.length})</a>
          <a href="#/course/${d.chapter.course_id}">返回课程</a>
        </div>
        <div class="tip">用鼠标选中任意文本，会弹出笔记输入框；保存后原文自动高亮，刷新页面依然保留。</div>
      </aside>
      <div id="reading-area">
        ${pages.map(p => sheetHTML(p, byPage[p], d.chapter.title_fr, d.chapter.scan_base)).join('')}
        <div class="card" id="exercises-jump">
          <div class="row">
            <div style="flex:1;min-width:220px">
              <div style="font-size:15px;font-weight:600;color:var(--blue-dark)">EXERCICES</div>
              <div style="font-size:13px;color:var(--muted)">共 ${d.exercise_count} 个大题，空格与方框已转换为在线控件；标准答案待导入，交卷后只保存作答。</div>
            </div>
            <a class="btn btn-primary" href="#/exercise/${id}">开始本章练习</a>
          </div>
        </div>
      </div>
    </div>`;

  applyHighlights(d.notes);
  const noteId = params.get('note');
  if (noteId) {
    const mark = app.querySelector(`mark.hl[data-note-id="${noteId}"]`);
    if (mark) {
      mark.scrollIntoView({ behavior: 'smooth', block: 'center' });
      mark.classList.add('flash');
      setTimeout(() => mark.classList.remove('flash'), 3000);
    }
  }
}

/* ---------- highlight engine: offsets are into the block's textContent ---------- */
function textNodes(root) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const out = [];
  let n;
  while ((n = walker.nextNode())) out.push(n);
  return out;
}

function applyHighlights(notes) {
  const byBlock = {};
  notes.forEach(n => {
    if (n.block_id) (byBlock[n.block_id] ||= []).push(n);
  });
  Object.entries(byBlock).forEach(([bid, list]) => {
    const blockEl = app.querySelector(`[data-block-id="${bid}"]`);
    const src = state.blocksById[bid];
    if (!blockEl || !src) return;
    blockEl.innerHTML = src.body_html;
    list.slice().sort((a, b) => b.start_offset - a.start_offset)
      .forEach(n => wrapRange(blockEl, n.start_offset, n.end_offset, n.id));
  });
}

function wrapRange(blockEl, start, end, noteId) {
  const targets = [];
  let pos = 0;
  for (const node of textNodes(blockEl)) {
    const ns = pos, ne = pos + node.nodeValue.length;
    if (ne > start && ns < end) {
      targets.push({ node, from: Math.max(start, ns) - ns, to: Math.min(end, ne) - ns });
    }
    pos = ne;
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
    } catch (_) { /* boundary edge case: skip this segment */ }
  }
}

function selectionOffsets(blockEl, range) {
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

function showPopover(rect, draft, existing) {
  state.pendingSelection = draft;
  state.editingNoteId = existing ? existing.id : null;
  popoverQuote.textContent = draft.quote;
  popoverText.value = existing ? existing.text : '';
  popoverSave.textContent = existing ? '更新笔记' : '保存笔记';
  popoverDelete.classList.toggle('hidden', !existing);
  const left = Math.min(rect.left + window.scrollX, window.scrollX + window.innerWidth - 340);
  popover.style.left = Math.max(window.scrollX + 8, left) + 'px';
  popover.style.top = (rect.bottom + window.scrollY + 10) + 'px';
  popover.classList.remove('hidden');
  popoverText.focus();
}

function hidePopover() {
  popover.classList.add('hidden');
  state.pendingSelection = null;
  state.editingNoteId = null;
}

document.addEventListener('mouseup', e => {
  if (!state.chapter || !parseHash().path.startsWith('/chapter/')) return;
  if (e.target.closest && e.target.closest('#note-popover')) return;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return;
  const range = sel.getRangeAt(0);
  const anchor = range.commonAncestorContainer.nodeType === 1
    ? range.commonAncestorContainer
    : range.commonAncestorContainer.parentElement;
  const blockEl = anchor && anchor.closest ? anchor.closest('.block') : null;
  if (!blockEl || !app.contains(blockEl)) return;
  const quote = sel.toString();
  if (!quote.trim()) return;
  const offsets = selectionOffsets(blockEl, range);
  if (!offsets || offsets.end <= offsets.start) return;
  const draft = {
    chapter_id: state.chapter.chapter.id,
    block_id: Number(blockEl.dataset.blockId),
    page_no: Number(blockEl.dataset.pageNo),
    start_offset: offsets.start,
    end_offset: offsets.end,
    quote,
  };
  showPopover(range.getBoundingClientRect(), draft, null);
});

popoverClose.addEventListener('click', hidePopover);
document.addEventListener('keydown', e => {
  if (e.key === 'Escape') hidePopover();
});

popoverSave.addEventListener('click', async () => {
  const text = popoverText.value.trim();
  if (!text) { toast('请先写下笔记内容'); return; }
  const wasEditing = !!state.editingNoteId;
  try {
    if (wasEditing) {
      await api(`/api/notes/${state.editingNoteId}`, { method: 'PUT', body: JSON.stringify({ text }) });
    } else {
      await api('/api/notes', { method: 'POST', body: JSON.stringify({ ...state.pendingSelection, text }) });
    }
    hidePopover();
    window.getSelection()?.removeAllRanges();
    await reloadChapterNotes();
    toast(wasEditing ? '笔记已更新' : '笔记已保存并高亮');
  } catch (err) { toast('保存失败：' + err.message); }
});

popoverDelete.addEventListener('click', async () => {
  if (!state.editingNoteId) return;
  try {
    await api(`/api/notes/${state.editingNoteId}`, { method: 'DELETE' });
    hidePopover();
    await reloadChapterNotes();
    toast('笔记已删除');
  } catch (err) { toast('删除失败：' + err.message); }
});

async function reloadChapterNotes() {
  const id = state.chapter.chapter.id;
  const d = await api(`/api/chapters/${id}`);
  state.chapter.notes = d.notes;
  applyHighlights(d.notes);
  const link = app.querySelector('.toc-page a[href="#/notes"]');
  if (link) link.textContent = `笔记汇总(${d.notes.length})`;
}

/* ------------------------------ exercises ------------------------------ */
// Placeholders are resolved into widgets at render time, so the stored stem
// keeps the original book text verbatim:
//   {{b}} short blank · {{L}} long line · {{T}} essay box
//   {{c:group}} tick box (same group = mutually exclusive)
//   {{o:A|B|C}} clickable options · {{o!:A|B}} single-choice options
function renderStem(html, qid, readonly = false, blanks = {}) {
  let i = 0;
  return html.replace(/\{\{([bLTco])(!?)(?::([^}]*))?\}\}/g, (_, kind, bang, arg) => {
    i++;
    const v = blanks[String(i)] || '';
    if (readonly) {
      if (kind === 'c' && v) return `<span class="given tick-given">✓</span>`;
      if (kind === 'o' && v) {
        const opts = (arg || '').split('|');
        const label = v.split(',').map(n => opts[Number(n) - 1] || n).join(' / ');
        return `<span class="given">${esc(label)}</span>`;
      }
      return `<span class="given">${v ? esc(v) : '______'}</span>`;
    }
    const attrs = `data-q="${qid}" data-idx="${i}" data-widget="${kind}"`;
    if (kind === 'b') {
      return `<input class="blank" ${attrs} autocomplete="off" spellcheck="false" aria-label="填空 ${i}">`;
    }
    if (kind === 'L') {
      return `<input class="blank line" ${attrs} autocomplete="off" spellcheck="false" aria-label="填空 ${i}">`;
    }
    if (kind === 'T') {
      return `<textarea class="blank essay" rows="2" ${attrs} spellcheck="false" aria-label="作答 ${i}"></textarea>`;
    }
    if (kind === 'c') {
      const group = `${qid}:${arg || ''}`;
      return `<button type="button" class="tick" ${attrs} data-group="${esc(group)}" data-value="" aria-label="勾选 ${i}">□</button>`;
    }
    if (kind === 'o') {
      const opts = (arg || '').split('|');
      const single = bang === '!' ? '1' : '0';
      const buttons = opts.map((o, k) =>
        `<button type="button" class="opt" data-opt="${k + 1}">${esc(o)}</button>`).join('<span class="opt-sep">|</span>');
      return `<span class="opt-group" ${attrs} data-single="${single}" data-value="">${buttons}</span>`;
    }
    return '';
  });
}

function widgetValue(el) {
  const w = el.dataset.widget;
  if (w === 'c' || w === 'o') return el.dataset.value || '';
  return el.value || '';
}

function questionHTML(q) {
  return `<div class="question ${esc(q.type)}" data-question="${q.id}">
    <div class="stem-text">${renderStem(q.stem_html, q.id)}</div>
  </div>`;
}

function exerciseHTML(ex) {
  const twoCols = ex.label && ex.questions.length === 2;
  let inner;
  if (twoCols) {
    inner = `<div class="blue-box two-cols"><span class="tab">${esc(ex.label)}</span>
      ${ex.questions.map(q => `<div class="question" data-question="${q.id}" style="border:0;padding:0">${renderStem(q.stem_html, q.id)}</div>`).join('')}
    </div>`;
  } else {
    inner = `<div>${ex.questions.map(questionHTML).join('')}</div>`;
  }
  const note = ex.note_html
    ? (ex.id === 10
      ? `<div class="blue-box" style="line-height:1.9;font-size:13px">${ex.note_html}</div>`
      : `<div class="ex-note">${ex.note_html}</div>`)
    : '';
  return `
    <section class="exercise-card">
      <div class="exercise-head">
        <div class="ex-num">${ex.seq}</div>
        <div>
          <div class="ex-instruction">${esc(ex.instruction_fr)}</div>
          <div class="ex-source">EXERCICES · ${esc(ex.source_ref)}</div>
        </div>
      </div>
      ${note}
      ${inner}
    </section>`;
}

let submitting = false;
async function viewExercise(id) {
  const d = await api(`/api/chapters/${id}/exercises`);
  setCourse(d.course_id);
  const questionCount = d.exercises.reduce((n, e) => n + e.questions.length, 0);
  const widgetCount = d.exercises.reduce((n, e) =>
    n + e.questions.reduce((m, q) => m + (q.stem_html.match(/\{\{[^}]+\}\}/g) || []).length, 0), 0);
  submitting = false;
  const byPage = {};
  d.exercises.forEach(e => (byPage[e.page_no] ||= []).push(e));
  const pages = Object.keys(byPage).map(Number).sort((a, b) => a - b);

  app.innerHTML = `
    ${pageHead('EXERCICES', `${esc(d.chapter_title_fr)} · ${d.exercises.length} 个大题 · ${questionCount} 小题 · ${widgetCount} 个作答控件`)}
    <div class="pending-box" style="margin-bottom:16px">
      标准答案尚未导入。现在交卷只会保存你的作答，不会判分；后续导入答案后可对历史作答一键重判。
    </div>
    ${pages.map(p => `
      <div class="page-group">
        <div class="sheet-head" style="border-radius:8px 8px 0 0"><span>${esc(d.chapter_title_fr)} · EXERCICES</span><span class="folio">${p} • ${FOLIO[p] || ''}</span></div>
        <div class="page-group-body">
          ${scanBlock(d.scan_base, p)}
          ${byPage[p].map(exerciseHTML).join('')}
        </div>
      </div>`).join('')}
    <div class="submit-bar">
      <div class="info" id="fill-info">已作答 0 / ${widgetCount} 处</div>
      <div class="spacer"></div>
      <a class="btn" href="#/chapter/${id}">返回章节</a>
      <button class="btn btn-primary" id="submit-btn" type="button">交卷并保存作答</button>
    </div>`;

  const widgets = [...app.querySelectorAll('[data-q][data-idx]')];
  const info = document.getElementById('fill-info');
  const update = () => {
    info.textContent = `已作答 ${widgets.filter(w => widgetValue(w).trim()).length} / ${widgetCount} 处`;
  };
  widgets.forEach(w => w.addEventListener('input', update));

  document.getElementById('submit-btn').addEventListener('click', async () => {
    if (submitting) return;
    submitting = true;
    const btn = document.getElementById('submit-btn');
    btn.disabled = true;
    btn.textContent = '正在保存…';
    try {
      const grouped = {};
      widgets.forEach(w => {
        const q = w.dataset.q, idx = w.dataset.idx;
        (grouped[q] ||= {})[idx] = widgetValue(w);
      });
      const answers = Object.entries(grouped).map(([question_id, blanks]) => ({
        question_id: Number(question_id), blanks,
      }));
      const created = await api('/api/attempts', {
        method: 'POST', body: JSON.stringify({ chapter_id: Number(id) }),
      });
      await api(`/api/attempts/${created.attempt_id}/submit`, {
        method: 'POST', body: JSON.stringify({ answers }),
      });
      location.hash = `#/result/${created.attempt_id}`;
    } catch (err) {
      toast('交卷失败：' + err.message);
      btn.disabled = false;
      btn.textContent = '交卷并保存作答';
      submitting = false;
    }
  });
}

/* ------------------------------ result ------------------------------ */
async function viewResult(id) {
  const d = await api(`/api/attempts/${id}`);
  const a = d.attempt;
  if (a.course_id) setCourse(a.course_id);
  const graded = a.grading_status === 'graded';
  app.innerHTML = `
    ${pageHead('交卷结果', `作答编号 #${a.id} · ${esc(a.submitted_at || '')}`)}
    <div class="card result-hero">
      ${graded
        ? `<div class="big">${Math.round(a.score ?? 0)} / ${Math.round(a.total ?? 0)}</div><div class="sub">已自动判分</div>`
        : `<div class="big">—</div><div class="sub">已保存 ${d.items.length} 道小题的作答</div>`}
    </div>
    ${graded ? '' : `<div class="pending-box" style="margin-bottom:16px">
      标准答案尚未导入，本次不判分。你填入的内容已完整保存；等你提供答案后，可在服务端导入并对历史作答重新判分。
    </div>`}
    <div class="card">
      <div class="section-label" style="margin-top:0">逐题作答记录</div>
      ${d.items.map(item => `
        <div class="answer-item">
          <div class="qref">${esc(item.instruction_fr)} · ${esc(item.source_ref)}
            ${graded ? `<span class="pill ${item.is_correct === 1 ? 'ok' : item.is_correct === 0 ? 'bad' : ''}">${item.is_correct === 1 ? '正确' : item.is_correct === 0 ? '错误' : '待判'}</span>` : '<span class="pill">待判</span>'}
          </div>
          <div style="margin-top:5px">${renderStem(item.stem_html, item.question_id, true, item.blanks)}</div>
        </div>`).join('')}
    </div>
    <div class="row" style="margin-top:16px">
      <a class="btn" href="#/exercise/${a.chapter_id || 1}">再做一次</a>
      <a class="btn" href="#/notes">查看笔记</a>
      <a class="btn" href="#/chapter/${a.chapter_id || 1}">回到章节</a>
      ${a.course_id ? `<a class="btn btn-primary" href="#/course/${a.course_id}">返回课程</a>` : ''}
    </div>`;
}

/* ------------------------------ notes ------------------------------ */
async function viewNotes() {
  const courses = await getCourses();
  app.innerHTML = `
    ${pageHead('笔记汇总', '支持按笔记内容或选中原文检索，点击「跳转原文」回到高亮位置')}
    <div class="search-bar">
      <input id="note-search" type="search" placeholder="搜索笔记内容或高亮原文…" value="${esc(state.noteQuery)}">
      <select id="note-course-filter" class="filter-select" aria-label="按课程筛选">
        <option value="">全部课程</option>
        ${courses.map(c => `<option value="${c.id}" ${String(state.noteCourse) === String(c.id) ? 'selected' : ''}>${c.code.startsWith('vocabulaire') ? '词汇' : '语法'} · ${esc(c.title_fr)}</option>`).join('')}
      </select>
      <button class="btn" id="note-clear" type="button">清空</button>
    </div>
    <div id="note-list"></div>`;
  const load = async () => {
    const qs = new URLSearchParams();
    if (state.noteQuery) qs.set('q', state.noteQuery);
    if (state.noteCourse) qs.set('course_id', state.noteCourse);
    const d = await api('/api/notes?' + qs.toString());
    const list = document.getElementById('note-list');
    if (!list) return;
    if (!d.notes.length) {
      list.innerHTML = `<div class="card empty">${state.noteQuery || state.noteCourse ? '没有匹配的笔记' : '还没有笔记。到章节学习页选中一段文字即可添加。'}</div>`;
      return;
    }
    list.innerHTML = `<div style="font-size:12px;color:var(--muted);margin-bottom:10px">共 ${d.total} 条笔记</div>` +
      d.notes.map(n => `
        <div class="note-item" data-note="${n.id}">
          <div class="note-quote">${esc(n.quote)}</div>
          <div class="note-text" id="note-text-${n.id}">${esc(n.text)}</div>
          <div id="note-edit-${n.id}" class="hidden">
            <textarea rows="3" style="width:100%;border:1px solid var(--line);border-radius:8px;padding:9px;font-size:13px" id="note-edit-input-${n.id}">${esc(n.text)}</textarea>
            <div style="margin-top:8px;display:flex;gap:8px">
              <button class="btn btn-primary btn-sm" data-action="note-save" data-id="${n.id}">保存</button>
              <button class="btn btn-sm" data-action="note-cancel" data-id="${n.id}">取消</button>
            </div>
          </div>
          <div class="note-meta">
            <span>${n.course_id === 2 ? '词汇' : '语法'} · ${esc(n.chapter_title)} · p. ${n.page_no ?? '—'}</span>
            <span>${esc((n.created_at || '').slice(0, 16).replace('T', ' '))}</span>
            <span class="spacer"></span>
            <button class="btn btn-sm" data-action="note-goto" data-id="${n.id}" data-chapter="${n.chapter_id}">跳转原文</button>
            <button class="btn btn-sm" data-action="note-edit" data-id="${n.id}">编辑</button>
            <button class="btn btn-sm btn-danger" data-action="note-delete" data-id="${n.id}">删除</button>
          </div>
        </div>`).join('');
  };
  await load();
  const input = document.getElementById('note-search');
  let timer;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      state.noteQuery = input.value.trim();
      await load();
    }, 220);
  });
  document.getElementById('note-course-filter').addEventListener('change', e => {
    state.noteCourse = e.target.value;
    load();
  });
  document.getElementById('note-clear').addEventListener('click', async () => {
    state.noteQuery = '';
    state.noteCourse = '';
    input.value = '';
    document.getElementById('note-course-filter').value = '';
    await load();
  });
}

/* ------------------------------ interactions ------------------------------ */
app.addEventListener('click', async e => {
  // scan-page toggles
  const scanBtn = e.target.closest('.scan-toggle');
  if (scanBtn) {
    const frame = app.querySelector(`[data-scan-frame="${scanBtn.dataset.scan}"]`);
    if (frame) {
      frame.classList.toggle('hidden');
      scanBtn.textContent = `对照原书 p. ${scanBtn.dataset.scan} ${frame.classList.contains('hidden') ? '▾' : '▴'}`;
    }
    return;
  }

  // tick boxes for Vrai / Faux
  const tick = e.target.closest('.tick');
  if (tick) {
    const group = tick.dataset.group;
    if (group) {
      app.querySelectorAll('.tick').forEach(t => {
        if (t.dataset.group === group) {
          t.dataset.value = '';
          t.classList.remove('checked');
          t.textContent = '□';
        }
      });
    }
    tick.dataset.value = '1';
    tick.classList.add('checked');
    tick.textContent = '✓';
    updateFillInfo();
    return;
  }

  // clickable option groups (Choisissez la ou les bonne(s) réponse(s))
  const opt = e.target.closest('.opt');
  if (opt) {
    const g = opt.closest('.opt-group');
    const n = opt.dataset.opt;
    const selected = g.dataset.value ? g.dataset.value.split(',') : [];
    if (g.dataset.single === '1') {
      selected.length = 0;
      g.querySelectorAll('.opt').forEach(o => o.classList.remove('selected'));
      selected.push(n);
      opt.classList.add('selected');
    } else if (opt.classList.contains('selected')) {
      const k = selected.indexOf(n);
      if (k >= 0) selected.splice(k, 1);
      opt.classList.remove('selected');
    } else {
      selected.push(n);
      opt.classList.add('selected');
    }
    g.dataset.value = selected.sort((a, b) => Number(a) - Number(b)).join(',');
    updateFillInfo();
    return;
  }

  // note actions (summary page, editing, jumping)
  const btn = e.target.closest('[data-action]');
  if (btn) {
    const id = btn.dataset.id;
    const action = btn.dataset.action;
    try {
      if (action === 'note-goto') {
        setCourseByChapter(btn.dataset.chapter);
        location.hash = `#/chapter/${btn.dataset.chapter}?note=${id}`;
      } else if (action === 'note-edit') {
        document.getElementById(`note-text-${id}`).classList.add('hidden');
        document.getElementById(`note-edit-${id}`).classList.remove('hidden');
      } else if (action === 'note-cancel') {
        document.getElementById(`note-text-${id}`).classList.remove('hidden');
        document.getElementById(`note-edit-${id}`).classList.add('hidden');
      } else if (action === 'note-save') {
        const text = document.getElementById(`note-edit-input-${id}`).value.trim();
        await api(`/api/notes/${id}`, { method: 'PUT', body: JSON.stringify({ text }) });
        toast('笔记已更新');
        await render();
      } else if (action === 'note-delete') {
        await api(`/api/notes/${id}`, { method: 'DELETE' });
        toast('笔记已删除');
        await render();
      }
    } catch (err) { toast('操作失败：' + err.message); }
    return;
  }

  const mark = e.target.closest('mark.hl');
  if (mark && state.chapter) {
    const note = state.chapter.notes.find(n => String(n.id) === mark.dataset.noteId);
    if (note) {
      showPopover(mark.getBoundingClientRect(), {
        chapter_id: state.chapter.chapter.id,
        block_id: note.block_id,
        page_no: note.page_no,
        start_offset: note.start_offset,
        end_offset: note.end_offset,
        quote: note.quote,
      }, note);
    }
  }
});

function setCourseByChapter(chapterId) {
  const n = Number(chapterId);
  setCourse(n >= 1000 ? 2 : 1);
}

function updateFillInfo() {
  const info = document.getElementById('fill-info');
  if (!info) return;
  const widgets = [...app.querySelectorAll('[data-q][data-idx]')];
  const filled = widgets.filter(w => widgetValue(w).trim()).length;
  const total = Number((info.textContent.match(/\/ (\d+)/) || [])[1]) || widgets.length;
  info.textContent = `已作答 ${filled} / ${total} 处`;
}

if (courseSwitch) {
  courseSwitch.addEventListener('change', e => {
    setCourse(e.target.value);
    location.hash = `#/course/${state.courseId}`;
  });
}

/* ------------------------------ router ------------------------------ */
async function render() {
  const { path, params } = parseHash();
  setNav(path);
  hidePopover();
  try {
    if (path === '/') await viewHome();
    else if (path.startsWith('/course/')) await viewCourse(path.split('/')[2] || 1);
    else if (path.startsWith('/chapters')) await viewChapters(path.split('/')[2] || state.courseId);
    else if (path.startsWith('/chapter/')) await viewChapter(path.split('/')[2] || 1, params);
    else if (path.startsWith('/exercise/')) await viewExercise(path.split('/')[2] || 1);
    else if (path.startsWith('/result/')) await viewResult(path.split('/')[2] || 0);
    else if (path === '/notes') await viewNotes();
    else await viewHome();
  } catch (err) {
    app.innerHTML = `<div class="card empty">加载失败：${esc(err.message)}</div>`;
  }
  await updateChrome(state.courseId);
  await refreshAnswerBadge();
}

window.addEventListener('hashchange', render);
render();
