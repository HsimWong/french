import {
  viewDictionary, openDrawer, drawerHide, showSelectionDrawer,
} from './dictionary.js';
import { keyPage as viewAnswerKey, saveKeyExercise } from './answer-key.js';
import { api, esc, pageHead, setCourse, state, toast, ui } from './ui-core.js';
import { applyHighlights, deleteNote, reloadChapterNotes, selectionOffsets } from './notes.js';
import { draftFromSelection, exerciseStemKey, loadStemNotes, paintStemNotes } from './exercise-notes.js';
import { renderNoteText } from './note-images.js';
import { loadHomeStats } from './stats-view.js';
import './study-tracker.js'; // side effect: active-time heartbeats
export { app, api, esc, pageHead, scanBlock, setCourse, toast };

const app = document.getElementById('app');
ui.app = app;
const popover = document.getElementById('note-popover');
const popoverQuote = document.getElementById('popover-quote');
const popoverText = document.getElementById('popover-text');
const popoverSave = document.getElementById('popover-save');
const popoverDelete = document.getElementById('popover-delete');
const popoverClose = document.getElementById('popover-close');
const courseSwitch = document.getElementById('course-switch');

// Shared on window because dictionary.js and answer-key.js need the same
// session: which course is selected and whether reading mode is active.

const FOLIO = {
  6: 'six', 7: 'sept', 8: 'huit', 9: 'neuf', 10: 'dix', 11: 'onze', 12: 'douze',
};

function parseHash() {
  const raw = location.hash.slice(1) || '/';
  const [path, qs] = raw.split('?');
  return { path: path || '/', params: new URLSearchParams(qs || '') };
}

async function getCourses() {
  return (await api('api/courses')).courses;
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
    if (r === '/dictionary') a.href = `#/dictionary?course=${cur.id}`;
  });
}

function setNav(path) {
  app.classList.toggle('notes-page', path === '/notes');
  document.querySelectorAll('.nav a[data-route]').forEach(a => {
    const r = a.dataset.route;
    let active = r === path;
    if (r === '/chapters') active = path.startsWith('/chapters');
    if (r === '/chapter') active = path.startsWith('/chapter/');
    if (r === '/exercise') active = path.startsWith('/exercise/');
    if (r === '/notes') active = path.startsWith('/notes');
    if (r === '/dictionary') active = path.startsWith('/dictionary');
    if (r === '/') active = path === '/' || path.startsWith('/course/');
    a.classList.toggle('active', active);
  });
}

async function refreshAnswerBadge() {
  try {
    const s = await api('api/answers/status');
    const el = document.getElementById('answer-badge');
    if (s.ready > 0) {
      el.innerHTML = `<span class="badge ready">答案已导入 ${s.ready}/${s.total}</span>`;
    } else {
      el.innerHTML = `<span class="badge">答案待导入 · ${s.total} 题</span>`;
    }
  } catch (_) { /* ignore */ }
}

// Jump from the notes list back to the exact highlight. Restarting the
// animation matters when the same note is opened twice in a row.
function flashNote(noteId) {
  const mark = app.querySelector(`mark.hl[data-note-id="${noteId}"]`);
  if (!mark) return false;
  mark.scrollIntoView({ behavior: 'smooth', block: 'center' });
  mark.classList.remove('flash');
  void mark.offsetWidth;
  mark.classList.add('flash');
  clearTimeout(mark._flashTimer);
  mark._flashTimer = setTimeout(() => mark.classList.remove('flash'), 4200);
  return true;
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
  // Reading blocks get ids and are rendered from the original HTML; the
  // dictionary drawer never inserts marks, so notes keep exact offsets.
  app.innerHTML = `
    ${pageHead('学习台', 'Grammaire progressive · Vocabulaire progressif — 两门课程，按原书逐字录入')}
    <div class="metrics">
      <div class="metric"><div class="label">课程</div><div class="value">${d.length}</div><div class="hint">语法 + 词汇</div></div>
      <div class="metric"><div class="label">章节总数</div><div class="value">${totalChapters}</div><div class="hint">按原书目录 1:1 建章</div></div>
      <div class="metric"><div class="label">已录入内容</div><div class="value">${totalReady}</div><div class="hint">${totalBlocks} 个讲解块</div></div>
      <div class="metric"><div class="label">笔记</div><div class="value">${totalNotes}</div><div class="hint">高亮笔记总数</div></div>
    </div>
    <div id="home-stats"></div>
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
    <div id="resume-slot"></div>
    <div class="card" style="margin-top:18px">
      <div class="tip" style="border:0;margin:0;padding:0">
        章节学习页选中任意文字即可添加高亮笔记；词汇教材插图较多，每页都可展开「对照原书」影印页。
      </div>
    </div>`;
  loadResumeCard('resume-slot');
  loadHomeStats();
}

/* ------------------------------ one course home ------------------------------ */
async function viewCourse(courseId) {
  setCourse(courseId);
  const d = await api(`api/course?course_id=${courseId}`);
  const courses = await getCourses();
  const cur = courses.find(c => c.id === Number(courseId));
  let resume = null;
  try { resume = (await api(`api/resume?course_id=${courseId}`)).resume; } catch (_) { resume = null; }
  const ready = d.chapters.filter(c => c.status === 'ready');
  const first = d.chapters[0];
  const isVocab = d.course.code.startsWith('vocabulaire');
  app.innerHTML = `
    ${pageHead(d.course.title_fr, `${d.course.level} · ${d.course.source_book}`)}
    ${resume ? resumeCardHTML(resume, `#/exercise/${resume.chapter_id}`) : ''}
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

/* ------------------------------ resume & revision history ------------------------------ */
function fmtTime(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso.slice(0, 16).replace('T', ' ');
  const pad = n => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// Source values written by the server; anything else is treated as a manual
// submission so a new label never leaves the table unreadable.
const SOURCE_LABELS = { submit: '交卷', auto: '自动保存', legacy: '旧版记录', import: '导入' };

// One resume card, used by both the learning home and the course page.
function resumeCardHTML(r, exerciseHref) {
  const isDraft = r.status === 'in_progress' && r.answered > 0;
  const pct = r.question_count ? Math.round(r.answered / r.question_count * 100) : 0;
  const link = exerciseHref || `#/exercise/${r.chapter_id}`;
  return `
    <div class="resume-card">
      <div class="resume-main">
        <div class="resume-kicker">${isDraft ? '继续上次答题' : '上次交卷'}</div>
        <div class="resume-title">第 ${r.chapter_seq} 讲 · ${esc(r.chapter_title)}</div>
        <div class="resume-meta">
          <span class="pill ${isDraft ? 'amber' : 'ok'}">${isDraft ? `草稿 v${r.draft_version}` : `已交卷 v${r.draft_version - 1}`}</span>
          ${isDraft
            ? `<span>已答 ${r.answered} / ${r.question_count} 题（${pct}%）</span>`
            : `<span>得分 ${r.total ? trimScore(r.score) + ' / ' + trimScore(r.total) : '待判'}</span>`}
          <span>最后保存 ${fmtTime(r.updated_at)}</span>
        </div>
        ${isDraft ? `<div class="resume-progress"><i style="width:${pct}%"></i></div>` : ''}
      </div>
      <div class="resume-actions">
        <a class="btn btn-primary" href="${link}">${isDraft ? '继续作答' : '再做一次 / 改答案'}</a>
        ${!isDraft ? `<a class="btn" href="#/result/${r.attempt_id}">查看成绩</a>` : ''}
        <a class="btn" href="${link}?history=1">${isDraft ? '版本记录' : '回到答题页'}</a>
      </div>
    </div>`;
}

async function loadResumeCard(slotId) {
  const slot = document.getElementById(slotId);
  if (!slot) return;
  try {
    const r = (await api('api/resume')).resume;
    if (!r) {
      slot.innerHTML = '';
      return;
    }
    slot.innerHTML = resumeCardHTML(r);
  } catch (_) {
    slot.innerHTML = '';
  }
}

// A version row is shared by the exercise page and the standalone archive page.
function revisionRow(rev, { current = false, link = true, action = true } = {}) {
  const graded = rev.grading_status === 'graded';
  const score = graded ? `${trimScore(rev.score)} / ${trimScore(rev.total)}` : '未判分';
  const src = SOURCE_LABELS[rev.source] || '交卷';
  const body = `
    <span class="rev-ver">${esc(rev.label)}</span>
    <span class="pill ${graded ? 'ok' : 'amber'}">${score}</span>
    ${current ? '<span class="pill blue">当前</span>' : ''}
    ${rev.discarded ? '<span class="pill bad">已作废</span>' : ''}
    <span class="rev-meta">${esc(src)} · ${rev.answered ?? 0} 题 · ${fmtTime(rev.created_at)}</span>
    ${action ? `<span class="rev-actions">
      ${link ? `<a class="btn btn-sm" href="#/revision/${rev.revision_id}">查看答案</a>` : ''}
      ${current && !rev.discarded ? `<a class="btn btn-sm" href="#/exercise/${rev.chapter_id}">重做这版</a>` : ''}
      ${current && !rev.discarded && rev.discarded !== true ? `<button type="button" class="btn btn-sm btn-danger" data-action="rev-discard" data-id="${rev.revision_id}">作废</button>` : ''}
    </span>` : ''}`;
  return `<div class="rev-row ${current ? 'current' : ''}">${body}</div>`;
}

function trimScore(v) {
  if (v == null) return '—';
  return String(Math.round(Number(v) * 100) / 100);
}

function plainStem(html) {
  return String(html || '').replace(/<[^>]*>/g, ' ').replace(/&nbsp;/g, ' ').replace(/\s+/g, ' ').trim();
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
  const d = await api(`api/course?course_id=${courseId}`);
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
  const d = await api(`api/chapters/${id}`);
  state.insideReader = true;
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
    ${pageHead(d.chapter.title_fr, `${esc(d.chapter.course_title_fr)} · 印刷页 ${d.chapter.page_start}–${d.chapter.page_end} · PDF 页 ${d.chapter.pdf_start}–${d.chapter.pdf_end} · 选中文字同时查词和记笔记`)}
    <div class="read-layout" id="read-layout">
      <aside class="toc">
        <h3>本章目录</h3>
        ${d.sections.map((s, i) => `<a href="#sheet-${s.page_no}">${i + 1}. ${esc(s.title_fr)}<br><span style="color:var(--faint);font-size:11px">p. ${s.page_no}</span></a>`).join('')}
        <div class="toc-page">
          <a href="#exercises-jump">EXERCICES · ${d.exercise_count} 个大题</a>
          <a href="#/notes">笔记汇总(${d.notes.length})</a>
          <a href="#/course/${d.chapter.course_id}">返回课程</a>
        </div>
        <div class="tip">正文默认不加任何词典标注。选中文本后，右侧会同时出示词典卡和笔记框；已有笔记仍以黄色高亮保留。</div>
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
  if (noteId) flashNote(noteId);
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
}

document.addEventListener('mouseup', e => {
  if (!state.insideReader || !state.chapter) return;
  if (e.target.closest && e.target.closest('#note-popover')) return;
  if (e.target.closest && e.target.closest('#dict-drawer')) return;
  // Clicking an existing note highlight opens that note, never a new lookup.
  if (e.target.closest && e.target.closest('mark.hl')) return;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return;
  const range = sel.getRangeAt(0);
  const anchor = range.commonAncestorContainer.nodeType === 1
    ? range.commonAncestorContainer
    : range.commonAncestorContainer.parentElement;
  // Exercise stems use a different anchor: the note is tied to the blank's
  // question, and offsets exclude the input widgets.
  const stemEl = anchor && anchor.closest ? anchor.closest('.stem-text[data-block-id]') : null;
  if (stemEl && app.contains(stemEl) && state.exercisePage) {
    const qRaw = stemEl.dataset.question;
    const draft = draftFromSelection(stemEl, range, {
      chapter_id: state.chapter.chapter.id,
      exercise_id: Number(stemEl.dataset.exercise),
      // An empty question marks the exercise instruction line: q0 is a real
      // foreign key, so null (not 0) is required to store that note.
      question_id: (qRaw === '' || qRaw == null) ? null : Number(qRaw),
    });
    if (draft) {
      try {
        showSelectionDrawer(range.getBoundingClientRect(), draft);
      } catch (err) {
        toast('查词面板出错：' + err.message);
      }
      return;
    }
  }
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
  // Any module failure must not look like a dead mouse: surface the actual
  // message, and allow a retry from the dictionary page.
  try {
    showSelectionDrawer(range.getBoundingClientRect(), draft);
  } catch (err) {
    toast('查词面板出错：' + err.message);
  }
});

document.addEventListener('keydown', e => {
  if (e.key === 'Escape') {
    drawerHide();
    hidePopover();
  }
});

popoverSave.addEventListener('click', async () => {
  const text = popoverText.value.trim();
  if (!text) { toast('请先写下笔记内容'); return; }
  const wasEditing = !!state.editingNoteId;
  try {
    await commitNote({ text, noteId: wasEditing ? state.editingNoteId : null, draft: state.pendingSelection });
    hidePopover();
    window.getSelection()?.removeAllRanges();
    await reloadChapterNotes();
    toast(wasEditing ? '笔记已更新' : '笔记已保存并高亮');
  } catch (err) { toast('保存失败：' + err.message); }
});

popoverDelete.addEventListener('click', async () => {
  if (!state.editingNoteId) return;
  try {
    await deleteNote(state.editingNoteId);
    hidePopover();
    await reloadChapterNotes();
    toast('笔记已删除');
  } catch (err) { toast('删除失败：' + err.message); }
});

/* ------------------------------ exercises ------------------------------ */
// Placeholders are resolved into widgets at render time, so the stored stem
// keeps the original book text verbatim:
//   {{b}} short blank · {{L}} long line · {{T}} essay box
//   {{c:group}} tick box (same group = mutually exclusive)
//   {{o:A|B|C}} clickable options · {{o!:A|B}} single-choice options
function renderStem(html, qid, readonly = false, blanks = {}, marks = null) {
  let i = 0;
  return html.replace(/\{\{([bLTco])(!?)(?::([^}]*))?\}\}/g, (_, kind, bang, arg) => {
    i++;
    const v = blanks[String(i)] || '';
    if (readonly) {
      // marks colours a saved/graded blank: ok green, wrong red struck out.
      const mk = marks ? ` ${marks[String(i)] === 'ok' ? 'ok' : marks[String(i)] === 'wrong' ? 'bad' : marks[String(i)] === 'empty' ? 'empty' : ''}` : '';
      if (kind === 'c' && v) return `<span class="given tick-given${mk}">✓</span>`;
      if (kind === 'o' && v) {
        const opts = (arg || '').split('|');
        const label = v.split(',').map(n => opts[Number(n) - 1] || n).join(' / ');
        return `<span class="given${mk}">${esc(label)}</span>`;
      }
      return `<span class="given${mk}">${v ? esc(v) : '______'}</span>`;
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

function questionHTML(q, ex) {
  // data-block-id turns the stem into a note anchor: the dictionary drawer and
  // note editor use the same handlers as reading blocks.
  const key = ex ? exerciseStemKey(ex.id, q.id) : 0;
  const anchor = ex
    ? ` data-block-id="${key}" data-exercise="${ex.id}" data-question="${q.id}"`
    : '';
  return `<div class="question ${esc(q.type)}" data-question="${q.id}" data-exercise="${ex?.id || ''}">
    <div class="stem-text"${anchor}>${renderStem(q.stem_html, q.id)}</div>
    ${questionSolution(q)}
  </div>`;
}

// The check appears only after this exercise was submitted by the learner.
// The key is then already in the API response; before that it is absent.
function questionSolution(q) {
  if (!q.answer || !Object.keys(q.answer).length) {
    // Only a submitted exercise can carry a solution block in the first place.
    return '';
  }
  const key = Object.keys(q.answer).sort((a, b) => Number(a) - Number(b))
    .map(k => `<span class="q-key-val">${esc(q.answer[k])}</span>`).join(' ');
  return `<div class="q-key" data-question="${q.id}">
    <span class="q-key-label">参考答案</span> ${key}
    <span class="q-key-answer" hidden>作答：<span class="q-key-user"></span></span>
    ${q.explanation ? `<div class="q-explain">${esc(q.explanation)}</div>` : ''}
  </div>`;
}

function fmtFraction(v) {
  const n = Number(v || 0);
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/\.?0+$/, '');
}

function sourceLabel(src) {
  return ({ submit: '交卷', auto: '自动保存' })[src] || src || '';
}

function solutionHTML(s) {
  const graded = s.grading_status === 'graded';
  const pct = graded && s.total ? Math.round(Number(s.score || 0) / Number(s.total) * 100) : null;
  const items = (s.items || []).map(item => {
    const verdicts = (s.verdicts || {})[String(item.question_id)] || {};
    return `<div class="answer-item">
      <div class="qref">${esc((item.stem_html || '').replace(/\{\{[^}]+\}\}/g, '').replace(/<[^>]+>/g, ' ').trim().slice(0, 70) || item.source_ref)}</div>
      <div class="solution-stem">${renderStem(item.stem_html, item.question_id, true, item.blanks, verdicts)}</div>
      <div class="solution-key"><span class="q-key-label">参考答案</span>
        ${Object.keys(item.correct || {}).sort((a, b) => Number(a) - Number(b)).map(k => `<span class="q-key-val">${esc(item.correct[k])}</span>`).join(' ')}
      </div>
      ${item.explanation ? `<div class="q-explain">${esc(item.explanation)}</div>` : ''}
    </div>`;
  }).join('');
  return `<details class="ex-solution-box" open>
    <summary><span>标准答案与判分</span>
      <span class="pill ${graded ? 'ok' : 'amber'}">${graded ? `${trimScore(s.score)} / ${trimScore(s.total)}` : '待答案'}</span>
      ${pct !== null ? `<span class="rev-meta">正确率 ${pct}%</span>` : ''}
      <span class="rev-meta">${esc(s.label || '')} · ${esc(sourceLabel(s.source))} · ${esc(fmtTime(s.created_at))}</span>
    </summary>
    ${items || '<div class="empty">这道大题还没有可回看的作答。</div>'}
  </details>`;
}

async function loadSectionSolutions(chapterID, exercises) {
  const pending = exercises.filter(e => e.submission);
  await Promise.all(pending.map(async ex => {
    const box = document.getElementById(`solution-${ex.id}`);
    if (!box) return;
    try {
      const d = await api(`api/chapters/${chapterID}/exercises/${ex.id}/submission`);
      box.innerHTML = d.submission ? solutionHTML(d.submission) : '';
    } catch (err) {
      box.innerHTML = `<div class="fb-pending">标准答案读取失败：${esc(err.message)}</div>`;
    }
  }));
}

// The stored stem with every answered widget marked in place. Using an
// inline marker (not a badge appended elsewhere) keeps the original line
// breaks and word order of the printed exercise intact.
function renderMarkedStem(item) {
  const verdicts = item.verdicts || {};
  let i = 0;
  return String(item.stem_html || '').replace(/\{\{([bLTco])(!?)(?::([^}]*))?\}\}/g, (_, kind, bang, arg) => {
    i++;
    const k = String(i);
    const has = Object.prototype.hasOwnProperty.call(item.blanks || {}, k);
    const v = item.blanks?.[k] ?? '';
    if (!has) return '<span class="fb-mark empty">_</span>';
    const cls = verdicts[k] === 'ok' ? 'ok' : verdicts[k] === 'wrong' ? 'bad' : verdicts[k] === 'empty' ? 'empty' : '';
    let label = v;
    if (kind === 'c') label = v ? '✓' : '□';
    if (kind === 'o') {
      const wanted = String(v || '').split(',').filter(Boolean).map(n => Number(n));
      label = (arg || '').split('|').map((o, j) => wanted.includes(j + 1) ? `<b>${esc(o)}</b>` : esc(o)).join(' ');
    }
    return `<span class="fb-mark ${cls}" data-v="${esc(verdicts[k] || '')}">${label === '' ? '—' : (kind === 'o' ? label : esc(label))}</span>`;
  });
}

// Per-blank explanation for a saved or loaded paper, beside the question text.
function renderInlineVerdicts(item) {
  const verdicts = item.verdicts || {};
  const keys = Object.keys(verdicts);
  if (!keys.length) return '';
  return `<div class="fb-inline">${keys.sort((a, b) => Number(a) - Number(b)).map(k => {
    const v = verdicts[k];
    const cls = v === 'ok' ? 'ok' : v === 'wrong' ? 'bad' : 'empty';
    return `<span class="fb-dot ${cls}">${esc(k)}</span>`;
  }).join('')}<span class="fb-inline-note">✓ 对 · × 错 · – 空</span></div>`;
}

// Shows a saved paper as one value per blank with a per-blank verdict. Blank
// numbers follow the stem's own {{...}} order.
function formatSummary(blanks, verdicts, hasKey) {
  const keys = Object.keys(blanks || {}).sort((a, b) => Number(a) - Number(b));
  if (!keys.length) return '';
  const parts = keys.map(k => {
    const v = (blanks[k] || '').trim();
    const verdict = (verdicts || {})[k];
    const cls = verdict === 'ok' ? 'ok' : verdict === 'wrong' ? 'bad' : verdict === 'empty' ? 'empty' : '';
    return `<span class="fb-blank ${cls}" title="${verdict === 'ok' ? '正确' : verdict === 'wrong' ? '错误' : verdict === 'empty' ? '未作答' : ''}">
      <b>${esc(k)}</b>${esc(v || '—')}</span>`;
  }).join('');
  const hint = hasKey ? '' : '<div class="fb-pending">尚无标准答案，暂未判分</div>';
  return `<div class="fb-row">${parts}</div>${hint}`;
}

/* An exercise whose questions refer to drawings / photos / cartoons shows the
   original printed page inline, not hidden behind a toggle. */
function inlineScan(scanBase, pages) {
  if (!scanBase || !pages || !pages.length) return '';
  return `<figure class="ex-figure">
    ${pages.map(p => `<img src="${scanBase}${p}.jpg" alt="原书印刷第 ${p} 页的插图" loading="lazy">`).join('')}
    <figcaption>原书插图 · 印刷页 ${pages.join(' / ')}（题目中的图画、表格与照片以原书为准）</figcaption>
  </figure>`;
}

function exerciseHTML(ex, scanBase) {
  const twoCols = ex.label && ex.questions.length === 2;
  let inner;
  if (twoCols) {
    inner = `<div class="blue-box two-cols"><span class="tab">${esc(ex.label)}</span>
      ${ex.questions.map(q => `<div class="question" data-question="${q.id}" data-exercise="${ex.id}" style="border:0;padding:0">
        <div class="stem-text" data-block-id="${exerciseStemKey(ex.id, q.id)}" data-exercise="${ex.id}" data-question="${q.id}">${renderStem(q.stem_html, q.id)}</div>
      </div>`).join('')}
    </div>`;
  } else {
    inner = `<div>${ex.questions.map(q => questionHTML(q, ex)).join('')}</div>`;
  }
  const note = ex.note_html
    ? (ex.id === 10
      ? `<div class="blue-box" style="line-height:1.9;font-size:13px">${ex.note_html}</div>`
      : `<div class="ex-note">${ex.note_html}</div>`)
    : '';
  // Per-exercise summaries were never populated; the version panel above is
  // the single place that shows saved answers, so no empty box is rendered.
  return `
    <section class="exercise-card" data-exercise="${ex.id}" id="exercise-${ex.id}">
      <div class="exercise-head">
        <div class="ex-num">${ex.seq}</div>
        <div>
          <div class="ex-instruction stem-text" data-block-id="${exerciseStemKey(ex.id, 0)}" data-exercise="${ex.id}" data-question="">${esc(ex.instruction_fr)}</div>
          <div class="ex-source">EXERCICES · ${esc(ex.source_ref)}</div>
        </div>
        ${exSectionBadge(ex)}
        <button type="button" class="btn btn-primary btn-sm ex-submit" data-action="submit-exercise"
          data-exercise="${ex.id}" data-seq="${ex.seq}">提交本大题并判分</button>
      </div>
      ${note}
      ${ex.has_image ? inlineScan(scanBase, ex.scan_pages) : ''}
      ${inner}
      <div class="ex-solution" id="solution-${ex.id}">${exSolutionLoading(ex)}</div>
    </section>`;
}

function exSectionBadge(ex) {
  const s = ex.submission;
  if (!s) return '<span class="ex-section-badge">未提交</span>';
  if (s.grading_status === 'graded') return `<span class="ex-section-badge ok">已判分 ${trimScore(s.score)}/${trimScore(s.total)}</span>`;
  return '<span class="ex-section-badge warn">已提交 · 待答案</span>';
}

function exSolutionLoading(ex) {
  return ex.submission ? '<div class="fb-pending">正在读取这道大题的标准答案…</div>' : '';
}

let submitting = false;
async function viewExercise(id) {
  const [d, draft] = await Promise.all([
    api(`api/chapters/${id}/exercises`), api(`api/chapters/${id}/attempt`),
  ]);
  setCourse(d.course_id);
  // The exercise sheet is a reading surface too: selection must open the
  // dictionary/note drawer, and note anchors live on question stems.
  state.exercisePage = { chapterId: Number(id) };
  state.insideReader = true;
  state.blocksById = {};
  state.stemNotes = [];
  state.chapter = {
    // Route params are strings; the notes API declares chapter_id as int.
    chapter: { id: Number(id), course_id: d.course_id, title_fr: d.chapter_title_fr },
    notes: [],
  };
  const questionCount = d.exercises.reduce((n, e) => n + e.questions.length, 0);
  const widgetCount = d.exercises.reduce((n, e) =>
    n + e.questions.reduce((m, q) => m + (q.stem_html.match(/\{\{[^}]+\}\}/g) || []).length, 0), 0);
  submitting = false;
  const byPage = {};
  d.exercises.forEach(e => (byPage[e.page_no] ||= []).push(e));
  const pages = Object.keys(byPage).map(Number).sort((a, b) => a - b);
  const saved = draft.answers || {};
  const savedQuestions = Object.keys(saved).filter(q => Object.keys(saved[q] || {}).some(k => (saved[q][k] || '').trim())).length;
  const dm = draft.draft_meta;
  const parsed = new URLSearchParams(location.hash.split('?')[1] || '');
  const loadedLabel = parsed.get('loaded') || '';
  const history = [...(draft.history || [])];
  if (dm) {
    // The draft row leads the list: it is the state being edited right now.
    const idx = history.findIndex(r => r.status === 'in_progress');
    if (idx >= 0) history.splice(idx, 1);
    history.unshift(dm);
  }
  const hasAnswerFor = d.exercises.flatMap(e => e.questions).filter(q => q.has_answer).length;
  const openHistory = parseHash().params.get('history') === '1';

  app.innerHTML = `
    ${pageHead(`EXERCICES · 第 ${d.chapter_seq} 讲`, `${esc(d.chapter_title_fr)} · ${d.exercises.length} 个大题 · ${questionCount} 小题 · ${widgetCount} 个作答控件`)}
    ${savedQuestions ? `<div class="resume-banner">
      <div>
        <strong>继续上次答题</strong>
        <span>${draft.graded ? '已载入最近交卷的作答，可直接修改；' : '已载入草稿；'}${savedQuestions} / ${questionCount} 题有内容，最后保存 ${fmtTime(draft.updated_at)}。</span>
      </div>
      <button type="button" class="btn btn-sm" data-action="scroll-first-saved">定位到第一题已答</button>
    </div>` : ''}
    ${loadedLabel ? `<div class="load-note">答题页暂为 <b>${esc(loadedLabel)}</b> 的内容；编辑后自动变为新草稿，交卷生成 <b>v${draft.draft_version}</b>。</div>` : ''}
    ${savedQuestions && !draft.graded && draft.answers && Object.keys(draft.answers).length ? `<div class="pending-box" style="margin-bottom:14px">
      这份草稿暂未判分；交卷时会按当前题库的 ${hasAnswerFor}/${questionCount} 道已有答案的题即时判分。
    </div>` : ''}
    <details class="hist-panel" id="hist-panel" ${openHistory ? 'open' : ''}>
      <summary>版本记录 <span class="pill">${history.length} 条</span><span class="hist-hint">每次交卷冻结一个版本，可随时回看答案与判分</span></summary>
      ${history.length ? `<div class="rev-rows">${history.map(r => `
        <div class="rev-row ${r.attempt_id === draft.attempt_id && r.status === 'submitted' ? 'current' : ''}">
          <span class="rev-ver">${esc(r.history_label || r.label || '')}</span>
          <span class="pill ${r.status === 'in_progress' ? 'blue' : r.grading_status === 'graded' ? 'ok' : 'amber'}">${r.status === 'in_progress' ? '正在编辑' : r.grading_status === 'graded' ? trimScore(r.score) + ' / ' + trimScore(r.total) : '未判分'}</span>
          <span class="rev-meta">${r.status === 'in_progress' ? `已答 ${r.answered} / ${r.question_count} 题 · ` : ''}${fmtTime(r.updated_at || r.created_at)} · 答案版本 ${esc(r.key_version || '—')}</span>
          ${r.status === 'submitted' ? `<a class="link-small" href="#/attempt/${r.attempt_id}">答案与判分 →</a>` : ''}
          ${r.status === 'submitted' ? `<button type="button" class="link-small link-btn" data-action="hist-load" data-attempt="${r.attempt_id}" data-label="${esc(r.history_label || r.label || '')}">载入到答题页</button>` : ''}
          ${r.status === 'submitted' && r.discarded ? '<span class="pill bad">已作废</span>' : ''}
        </div>`).join('')}</div>` : '<div class="rev-empty">还没有作答记录。开始填写后会自动保存草稿，交卷会生成第一个版本。</div>'}
      <div class="rev-actions">
        <button type="button" class="btn btn-sm" data-action="hist-regrade">按当前答案重判</button>
        <button type="button" class="btn btn-sm btn-danger" data-action="restart">清空并重新作答</button>
      </div>
    </details>
    ${hasAnswerFor === 0 ? `<div class="pending-box" style="margin-bottom:16px">
      本站尚未录入本题的标准答案。交卷会冻结并保存你的作答版本，但暂不判分。
    </div>` : ''}
    ${pages.map(p => `
      <div class="page-group">
        <div class="sheet-head" style="border-radius:8px 8px 0 0"><span>${esc(d.chapter_title_fr)} · EXERCICES</span><span class="folio">${p} • ${FOLIO[p] || ''}</span></div>
        <div class="page-group-body">
          ${scanBlock(d.scan_base, p)}
          ${byPage[p].map(e => exerciseHTML(e, d.scan_base)).join('')}
        </div>
      </div>`).join('')}
    <div class="submit-bar">
      <div class="info" id="fill-info">已作答 0 / ${widgetCount} 处</div>
      <span class="save-state" id="save-state"></span>
      <div class="spacer"></div>
      <a class="btn" href="#/chapter/${id}">返回章节</a>
      <a class="btn btn-sm" href="#/admin/answers/${id}">录入答案</a>
      <button type="button" class="btn btn-sm" data-action="open-history">版本记录</button>
      <button class="btn" id="save-btn" type="button">保存草稿</button>
      <button class="btn btn-primary" id="submit-btn" type="button">交卷并生成版本</button>
    </div>`;

  // Every already-submitted exercise shows its own key and verdicts; before
  // that the API omits the key values entirely.
  await loadSectionSolutions(id, d.exercises);

  // Paint notes that were written against question stems in earlier visits.
  const stems = await loadStemNotes(id);
  state.chapter.notes = stems;
  paintStemNotes(app, stems);
  const noteId = parseHash().params.get('note');
  if (noteId) flashNote(noteId);

  const widgets = [...app.querySelectorAll('[data-q][data-idx]')];
  const info = document.getElementById('fill-info');
  const saveState = document.getElementById('save-state');
  const saveBtn = document.getElementById('save-btn');

  // Restore the last saved state: typed text, ticks, and choice groups.
  widgets.forEach(w => {
    const v = (saved[w.dataset.q] || {})[w.dataset.idx] || '';
    if (!v) return;
    if (w.dataset.widget === 'c') {
      w.dataset.value = '1';
      w.classList.add('checked');
      w.textContent = '✓';
    } else if (w.dataset.widget === 'o') {
      const chosen = v.split(',').filter(Boolean);
      w.dataset.value = chosen.join(',');
      w.querySelectorAll('.opt').forEach(o => {
        o.classList.toggle('selected', chosen.includes(o.dataset.opt));
      });
    } else {
      w.value = v;
    }
  });

  const collect = () => {
    const grouped = {};
    widgets.forEach(w => {
      const q = w.dataset.q, idx = w.dataset.idx;
      (grouped[q] ||= {})[idx] = widgetValue(w);
    });
    return Object.entries(grouped).map(([question_id, blanks]) => ({
      question_id: Number(question_id), blanks,
    }));
  };
  const countAnswered = () => widgets.filter(w => widgetValue(w).trim()).length;
  const update = () => { info.textContent = `已作答 ${countAnswered()} / ${widgetCount} 处`; };
  update();
  widgets.forEach(w => w.addEventListener('input', update));

  const setSaveState = (text, kind = '') => {
    if (!saveState) return;
    saveState.textContent = text;
    saveState.className = 'save-state ' + kind;
  };
  // 自动保存：停止输入 1.2 秒后写草稿；同样的内容不重复提交。
  // 只写草稿，不生成版本，交卷才冻结。
  const AUTOSAVE_MS = 1200;
  let dirty = false;
  let saving = false;
  let timer = 0;
  // A response that arrives after this page left must not repaint the bar of
  // another page (or a reloaded exercise), hence the token check.
  const pageToken = {};
  state.exercisePage = pageToken;
  const saveDraft = async ({ force = false } = {}) => {
    if (state.exercisePage !== pageToken) return false;
    if (!dirty && !force) return true;
    if (saving) return false;
    saving = true;
    setSaveState('正在保存草稿…', 'saving');
    try {
      const r = await api(`api/chapters/${id}/draft`, {
        method: 'PUT', body: JSON.stringify({ answers: collect() }),
      });
      if (state.exercisePage !== pageToken) return true;
      dirty = false;
      setSaveState(`草稿已保存 ${fmtTime(r.saved_at)} · 交卷将生成 v${r.draft_version}`, 'ok');
      return true;
    } catch (err) {
      if (state.exercisePage !== pageToken) return false;
      setSaveState('草稿保存失败：' + err.message, 'bad');
      return false;
    } finally {
      saving = false;
    }
  };
  const markDirty = () => {
    dirty = true;
    setSaveState('有未保存的修改…', 'pending');
    clearTimeout(timer);
    timer = setTimeout(saveDraft, AUTOSAVE_MS);
  };
  widgets.forEach(w => {
    w.addEventListener('input', markDirty);
    w.addEventListener('change', markDirty);
  });
  saveBtn.addEventListener('click', async () => {
    clearTimeout(timer);
    saveBtn.disabled = true;
    await saveDraft({ force: true });
    saveBtn.disabled = false;
  });
  window.addEventListener('beforeunload', e => {
    if (dirty && state.exercisePage === pageToken) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
  // One document-level listener survives tab switches; it always saves the
  // page that is currently on screen, never the one that was left behind.
  if (!state.visibilityBound) {
    state.visibilityBound = true;
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') state.exerciseSave?.();
    });
  }
  state.exerciseSave = () => {
    if (state.exercisePage !== pageToken) return;
    clearTimeout(timer);
    saveDraft();
  };

  document.getElementById('submit-btn').addEventListener('click', async () => {
    if (submitting) return;
    // Include any edit that was still waiting on the auto-save timer.
    clearTimeout(timer);
    submitting = true;
    const btn = document.getElementById('submit-btn');
    btn.disabled = true;
    btn.textContent = '正在交卷…';
    try {
      const created = await api('api/attempts', {
        method: 'POST', body: JSON.stringify({ chapter_id: Number(id) }),
      });
      const res = await api(`api/attempts/${created.attempt_id}/submit`, {
        method: 'POST', body: JSON.stringify({ answers: collect() }),
      });
      dirty = false;
      toast(`${res.version_label} 已保存${res.grading_status === 'graded' ? '并判分' : '，暂未判分'}`);
      location.hash = `#/result/${created.attempt_id}`;
    } catch (err) {
      toast('交卷失败：' + err.message);
      btn.disabled = false;
      btn.textContent = '交卷并生成版本';
      submitting = false;
    }
  });

  // Submit one exercise as its own graded section. Only this card's answers
  // are sent; the rest of the chapter can stay untouched.
  state.exerciseSectionSubmit = async (btn) => {
    const eid = Number(btn.dataset.exercise);
    const card = document.getElementById(`exercise-${eid}`);
    if (!card) return;
    const sectionInputs = [...card.querySelectorAll('[data-q][data-idx]')];
    if (!sectionInputs.some(w => widgetValue(w).trim())) { toast('这道大题还没有作答'); return; }
    if (!confirm(`提交第 ${btn.dataset.seq} 大题并立即判分？其他大题不受影响。`)) return;
    btn.disabled = true;
    const label = btn.textContent;
    btn.textContent = '判分中…';
    try {
      clearTimeout(timer);
      await saveDraft({ force: true });
      const byQuestion = {};
      sectionInputs.forEach(w => {
        (byQuestion[w.dataset.q] ||= {})[w.dataset.idx] = widgetValue(w);
      });
      const answers = Object.entries(byQuestion).map(([question_id, blanks]) => ({ question_id: Number(question_id), blanks }));
      const r = await api(`api/chapters/${id}/exercises/${eid}/submit`, {
        method: 'POST', body: JSON.stringify({ answers }),
      });
      toast(`第 ${btn.dataset.seq} 大题已提交：${r.grading_status === 'graded' ? fmtFraction(r.score) + ' / ' + fmtFraction(r.total) : '待答案'}`);
      await render();
    } catch (err) {
      toast('提交本大题失败：' + err.message);
      btn.disabled = false;
      btn.textContent = label;
    }
  };
}

/* ------------------------------ result ------------------------------ */
async function viewResult(id) {
  if (!Number(id)) {
    app.innerHTML = `${pageHead('交卷结果', '还没有可供查看的作答记录')}
      <div class="card empty">从「在线练习」交卷后，这里会显示该次作答的版本与判分结果。</div>`;
    return;
  }
  const d = await api(`api/attempts/${id}`);
  const a = d.attempt;
  if (a.course_id) setCourse(a.course_id);
  const graded = a.grading_status === 'graded';
  // A frozen version carries marks from the exact moment it was graded;
  // those stay attached to the version even after a later regrade.
  let versionItems = d.items;
  if (a.submitted_version && a.grading_status) {
    try {
      const h = (await api(`api/chapters/${a.chapter_id}/history`)).history || [];
      const cur = h.find(r => r.status === 'submitted' && !r.discarded && r.version === a.submitted_version);
      if (cur?.revision_id) {
        const v = await api(`api/revisions/${cur.revision_id}`);
        if ((v.items || []).length) versionItems = v.items;
      }
    } catch (_) { /* fall back to the API result */ }
  }
  const pct = graded && a.total ? Math.round((a.score ?? 0) / a.total * 100) : null;
  const versionPill = a.submitted_version ? `<span class="pill blue">已冻结为该作答的 v${a.submitted_version}</span>` : '';
  app.innerHTML = `
    ${pageHead('交卷结果', `作答编号 #${a.id} · ${esc(fmtTime(a.submitted_at))}`)}
    <div class="card result-hero">
      ${graded
        ? `<div class="big">${trimScore(a.score)} <span>/ ${trimScore(a.total)}</span></div>
           <div class="sub">已自动判分 · 正确率 ${pct}% · ${d.items.length} 道小题已保存</div>`
        : `<div class="big">—</div><div class="sub">已冻结并保存 ${d.items.length} 道小题的作答，尚未判分</div>`}
      ${versionPill}
    </div>
    ${graded ? '' : `<div class="pending-box" style="margin-bottom:16px">
      标准答案尚未导入，本版不判分。作答已完整冻结；导入答案后可在「在线练习 → 版本记录 → 按当前答案重判」一键重判，版本号与作答内容都不会变。
    </div>`}
    <div class="card">
      <div class="section-label" style="margin-top:0">逐题作答记录 <span class="hist-hint">绿 = 全对，红 = 有错，灰 = 未判分</span></div>
      ${versionItems.map(item => `
        <div class="answer-item">
          <div class="qref">${esc(item.instruction_fr)} · ${esc(item.source_ref)}
            ${graded ? `<span class="pill ${item.is_correct === 1 ? 'ok' : item.is_correct === 0 ? 'bad' : ''}">${item.is_correct === 1 ? '正确' : item.is_correct === 0 ? '错误' : '待判'}</span>` : '<span class="pill">待判</span>'}
            ${(item.verdicts && Object.keys(item.verdicts).length) ? '<span class="hist-hint">空位颜色标记对错</span>' : ''}
          </div>
          <div style="margin-top:5px">${renderStem(item.stem_html, item.question_id, true, item.blanks, item.verdicts)}</div>
          ${(item.verdicts && Object.keys(item.verdicts).length) ? `<div class="hist-hint" style="font-size:11px;margin-top:3px">划线空位 = 答错（对 ${Object.values(item.verdicts).filter(v => v === 'ok').length} · 空 ${Object.values(item.verdicts).filter(v => v === 'empty').length} · 错 ${Object.values(item.verdicts).filter(v => v === 'wrong').length}）</div>` : ''}
        </div>`).join('')}
    </div>
    <div class="row" style="margin-top:16px">
      <a class="btn btn-primary" href="#/exercise/${a.chapter_id || 1}">继续 / 修改这份答案</a>
      <a class="btn" href="#/exercise/${a.chapter_id || 1}?history=1">版本记录</a>
      <a class="btn" href="#/notes">查看笔记</a>
      <a class="btn" href="#/chapter/${a.chapter_id || 1}">回到章节</a>
      ${a.course_id ? `<a class="btn" href="#/course/${a.course_id}">返回课程</a>` : ''}
    </div>`;
}

/* ------------------------------ one frozen version ------------------------------ */
// Version pages are read-only. A version only becomes editable once it is
// explicitly loaded into the live draft, so history can never be rewritten.
async function viewRevision(revisionId) {
  const d = await api(`api/revisions/${revisionId}`);
  const r = d.revision;
  const at = await api(`api/attempts/${r.attempt_id}`);
  const a = at.attempt;
  if (a.course_id) setCourse(a.course_id);
  const graded = r.grading_status === 'graded';
  let lastEx = null;
  const cards = d.items.map(item => {
    const head = item.exercise_id !== lastEx
      ? `<div class="rev-ex-head">练习 ${esc((item.source_ref || '').split(',')[1] || '')} · p. ${item.page_no} · ${esc(item.instruction_fr)}</div>`
      : '';
    lastEx = item.exercise_id;
    const keys = Object.keys(item.blanks || {}).sort((x, y) => Number(x) - Number(y));
    return `${head}
      <div class="answer-item">
        <div class="qref">${esc(item.source_ref)}
          ${graded ? '<span class="pill ok">已判分</span>' : '<span class="pill">未判分</span>'}
        </div>
        <div class="stem-text" style="margin-top:4px">${renderMarkedStem(item)}</div>
        <div class="rev-answer">作答：${keys.map(k => `<span class="fb-blank">${esc(k)}. ${esc(item.blanks[k] || '—')}</span>`).join('') || '（未作答）'}</div>
        ${renderInlineVerdicts(item)}
        ${item.explanation ? `<div class="q-explain">${esc(item.explanation)}</div>` : ''}
      </div>`;
  }).join('');
  app.innerHTML = `
    ${pageHead(`${r.label} 作答存档`, `${esc(fmtTime(r.created_at))} · 冻结后不可修改`)}
    <div class="card result-hero" style="display:flex;gap:28px;align-items:center;justify-content:center;flex-wrap:wrap">
      <div>
        <div class="big">${graded ? `${trimScore(r.score)} <span>/ ${trimScore(r.total)}</span>` : '—'}</div>
        <div class="sub">${graded ? '该版本判分结果' : '该版本尚未判分'} · ${r.answered} 题</div>
      </div>
      <div class="rev-facts">
        <div>来源：${SOURCE_LABELS[r.source] || '交卷'}</div>
        <div>版本号：${esc(r.label)}</div>
        <div>只有「载入到答题页」后才会成为新的草稿</div>
      </div>
    </div>
    <div class="card">
      <div class="section-label" style="margin-top:0">版本内全部作答</div>
      ${cards || '<div class="empty">这一版没有可回看的作答内容（可能是旧版遗留记录）。</div>'}
    </div>
    <div class="row" style="margin-top:16px">
      <button type="button" class="btn btn-primary" data-action="hist-load" data-attempt="${r.attempt_id}" data-label="${esc(r.label)}">载入到答题页（生成下一版）</button>
      <a class="btn" href="#/exercise/${a.chapter_id || 1}?history=1">返回版本记录</a>
      <a class="btn" href="#/result/${r.attempt_id}">标准结果页</a>
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
    const d = await api('api/notes?' + qs.toString());
    const list = document.getElementById('note-list');
    if (!list) return;
    if (!d.notes.length) {
      list.innerHTML = `<div class="card empty">${state.noteQuery || state.noteCourse ? '没有匹配的笔记' : '还没有笔记。到章节学习页选中一段文字即可添加。'}</div>`;
      return;
    }
    list.innerHTML = `<div class="note-count">共 ${d.total} 条笔记</div>` +
      d.notes.map(n => `
        <div class="note-item" data-note="${n.id}" title="${esc(`${n.course_id === 2 ? '词汇' : '语法'} · ${n.chapter_title}${n.question_id ? ' · 练习' : ` · p. ${n.page_no ?? '—'}`} · ${n.created_at || ''}`)}">
          <div class="note-row">
            <span class="note-quote" title="${esc(n.quote)}">${esc(n.quote)}</span>
            <span class="note-text" id="note-text-${n.id}" title="${esc(n.text)}">${renderNoteText(n.text)}</span>
            <span class="note-meta">${n.course_id === 2 ? '词汇' : '语法'} · ${esc(n.chapter_title)}${n.question_id ? ' · 练习' : ` · p. ${n.page_no ?? '—'}`} · ${esc((n.created_at || '').slice(5, 16).replace('T', ' '))}</span>
            <span class="note-actions">
              <button class="btn btn-sm" title="跳转原文" data-action="note-goto" data-id="${n.id}" data-chapter="${n.chapter_id}" data-stem="${n.question_id ? 1 : 0}">跳转</button>
              <button class="btn btn-sm" data-action="note-edit" data-id="${n.id}">编辑</button>
              <button class="btn btn-sm btn-danger" data-action="note-delete" data-id="${n.id}">删除</button>
            </span>
          </div>
          <div id="note-edit-${n.id}" class="note-editor hidden">
            <textarea rows="3" id="note-edit-input-${n.id}">${esc(n.text)}</textarea>
            <div class="note-editor-actions">
              <button class="btn btn-primary btn-sm" data-action="note-save" data-id="${n.id}">保存</button>
              <button class="btn btn-sm" data-action="note-cancel" data-id="${n.id}">取消</button>
            </div>
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
    tick.dispatchEvent(new Event('input', { bubbles: true }));
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
    g.dispatchEvent(new Event('input', { bubbles: true }));
    return;
  }

  // note actions (summary page, editing, jumping)
  const btn = e.target.closest('[data-action]');
  if (btn) {
    const id = btn.dataset.id;
    const action = btn.dataset.action;
    try {
      if (action === 'hist-regrade') {
        const ch = parseHash().path.split('/')[2];
        if (!confirm('按当前已导入的答案重新判分本章所有历史版本？作答内容不会改变，只更新分数。')) return;
        const r = await api(`api/chapters/${ch}/regrade`, { method: 'POST', body: '{}' });
        toast(`已按当前答案重判 ${r.revisions} 个版本，${r.changed} 个分数有变化`);
        await render();
      } else if (action === 'restart') {
        if (!confirm('清空答题页并从头作答？当前草稿会作为「自动保存」版本留在版本记录中，可随时回看。')) return;
        const ch = parseHash().path.split('/')[2];
        const r = await api(`api/chapters/${ch}/restart`, { method: 'POST', body: '{}' });
        toast(r.message);
        await render();
      } else if (action === 'scroll-first-saved') {
        const first = app.querySelector('[data-q][data-idx]');
        const filled = [...app.querySelectorAll('[data-q][data-idx]')].find(w => widgetValue(w).trim());
        const target = filled || first;
        if (target) target.scrollIntoView({ behavior: 'smooth', block: 'center' });
      } else if (action === 'open-history') {
        const panel = document.getElementById('hist-panel');
        if (panel) {
          panel.open = true;
          panel.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
      } else if (action === 'submit-exercise') {
        await state.exerciseSectionSubmit?.(btn);
      } else if (action === 'hist-load') {
        const ch = parseHash().path.split('/')[2];
        const attemptId = Number(btn.dataset.attempt);
        if (!confirm(`把 ${btn.dataset.label || '这个版本'} 的答案载入答题页？将覆盖当前未交卷的草稿（原草稿会作为自动版本保存在记录中）。`)) return;
        const r = await api(`api/chapters/${ch}/load-version`, {
          method: 'POST', body: JSON.stringify({ attempt_id: attemptId }),
        });
        location.hash = `#/exercise/${ch}?loaded=${encodeURIComponent(r.loaded_label)}`;
      } else if (action === 'rev-discard') {
        if (!confirm('作废这个版本？它不会参与后续判分，但仍可在历史记录中回看。')) return;
        await api(`api/revisions/${id}/discard`, { method: 'POST', body: '{}' });
        toast('该版本已作废');
        await render();
      } else if (action === 'key-save-ex') {
        await saveKeyExercise(Number(btn.dataset.ex));
      } else if (action === 'note-goto') {
        setCourseByChapter(btn.dataset.chapter);
        // Stem notes belong to the exercise sheet, not the reading page.
        location.hash = btn.dataset.stem === '1'
          ? `#/exercise/${btn.dataset.chapter}?note=${id}`
          : `#/chapter/${btn.dataset.chapter}?note=${id}`;
      } else if (action === 'note-edit') {
        document.getElementById(`note-text-${id}`).classList.add('hidden');
        document.getElementById(`note-edit-${id}`).classList.remove('hidden');
      } else if (action === 'note-cancel') {
        document.getElementById(`note-text-${id}`).classList.remove('hidden');
        document.getElementById(`note-edit-${id}`).classList.add('hidden');
      } else if (action === 'note-save') {
        const text = document.getElementById(`note-edit-input-${id}`).value.trim();
        await api(`api/notes/${id}`, { method: 'PUT', body: JSON.stringify({ text }) });
        toast('笔记已更新');
        await render();
      } else if (action === 'note-delete') {
        await api(`api/notes/${id}`, { method: 'DELETE' });
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
      openDrawer({
        quote: note.quote,
    query: note.quote.trim().split(/\s+/)[0],
        note,
      });
      state.pendingSelection = {
        chapter_id: state.chapter.chapter.id,
        block_id: note.block_id,
        page_no: note.page_no,
        start_offset: note.start_offset,
        end_offset: note.end_offset,
        quote: note.quote,
      };
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
  drawerHide();
  // Leaving the answer sheet cancels pending auto-saves from it.
  state.exercisePage = null;
  state.insideReader = false;
  try {
    if (path === '/') await viewHome();
    else if (path.startsWith('/course/')) await viewCourse(path.split('/')[2] || 1);
    else if (path.startsWith('/chapters')) await viewChapters(path.split('/')[2] || state.courseId);
    else if (path.startsWith('/chapter/')) await viewChapter(path.split('/')[2] || 1, params);
    else if (path.startsWith('/exercise/')) await viewExercise(path.split('/')[2] || 1);
    else if (path.startsWith('/result/')) await viewResult(path.split('/')[2] || 0);
    else if (path.startsWith('/attempt/')) await viewResult(path.split('/')[2] || 0);
    else if (path.startsWith('/revision/')) await viewRevision(path.split('/')[2] || 0);
    else if (path.startsWith('/admin/answers')) await viewAnswerKey(path.split('/')[3] || 1);
    else if (path === '/notes') await viewNotes();
    else if (path === '/dictionary') await viewDictionary(params);
    else await viewHome();
  } catch (err) {
    app.innerHTML = `<div class="card empty">加载失败：${esc(err.message)}</div>`;
  }
  await updateChrome(state.courseId);
  await refreshAnswerBadge();
}

// Show the logout control only when a passcode is configured, and wire it up.
async function initAuth() {
  const btn = document.getElementById('auth-logout');
  if (!btn) return;
  try {
    const st = await api('api/auth/status');
    if (!st.enabled) return;
    btn.classList.remove('hidden');
    btn.addEventListener('click', async () => {
      try { await api('api/auth/logout', { method: 'POST' }); } catch (_) { /* ignore */ }
      location.reload();
    });
  } catch (_) { /* ignore */ }
}

window.addEventListener('hashchange', render);
render();
initAuth();
