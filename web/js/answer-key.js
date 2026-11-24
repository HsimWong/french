// Answer-key entry for exercise pages. The platform's keyboard import is
// /api/admin/answers; this page makes it usable without curl.

import { api, esc, pageHead, scanBlock, setCourse, toast } from './ui-core.js';

let keySession = null;
let inputBound = false;

function kq(sel, root = document) { return root.querySelector(sel); }
function kqa(sel, root = document) { return [...root.querySelectorAll(sel)]; }

// The key editor shows several accepted variants as "a / b"; send them back
// as an array so a round trip cannot turn them into one literal string.
function keyValue(raw) {
  const s = String(raw ?? '').trim();
  if (!s) return '';
  return s.includes(' / ') ? s.split(' / ').map(x => x.trim()).filter(Boolean) : s;
}

function plainStem(html) {
  return String(html || '').replace(/\{\{[^}]*\}\}/g, ' ____ ').replace(/<[^>]*>/g, ' ')
    .replace(/&nbsp;/g, ' ').replace(/\s+/g, ' ').trim();
}

function keyBlankSlots(q) {
  const n = (q.stem_html.match(/\{\{[^}]+\}\}/g) || []).length;
  if (!n) return '<span class="key-note">没有填空控件</span>';
  let out = '';
  for (let i = 1; i <= n; i++) {
    out += `<label class="key-slot"><b>${i}</b>
      <input class="key-input" data-blank="${i}" value="${esc(q.ex_answer?.[String(i)] || '')}" autocomplete="off" spellcheck="false" aria-label="第 ${i} 空答案"></label>`;
  }
  return out;
}

function keyCardHTML(ex, scanBase) {
  const head = scanBase ? scanBlock(scanBase, ex.page_no) : '';
  // Keep a snapshot of the stored key: an input the user emptied must still
  // be sent to the API, otherwise a "save" silently keeps the old answer.
  const initial = q => ({
    blanks: Object.fromEntries(Object.entries(q.ex_answer || {})
      .map(([k, v]) => [k, keyValue(v)]).filter(([, v]) => Array.isArray(v) || v)),
    explanation: q.explanation || '',
  });
  return `
    <section class="exercise-card key-card" data-key-ex="${ex.id}">
      <div class="exercise-head">
        <div class="ex-num">${ex.seq}</div>
        <div style="flex:1">
          <div class="ex-instruction">${esc(ex.instruction_fr)}</div>
          <div class="ex-source">${esc(ex.source_ref)}</div>
        </div>
        <div class="key-ex-actions">
          <button type="button" class="btn btn-sm" data-action="key-save-ex" data-ex="${ex.id}">保存本大题</button>
        </div>
      </div>
      ${head}
      ${ex.questions.map(q => `
        <div class="key-q" data-key-q="${q.id}" data-initial="${esc(JSON.stringify(initial(q)))}">
          <div class="key-stem">
            <span class="pill ${q.has_answer ? 'ok' : ''}">${q.has_answer ? '已有答案' : '待录入'}</span>
            <span class="key-stem-text">${esc(plainStem(q.stem_html))}</span>
            <span class="key-qref">${esc(q.source_ref)}</span>
          </div>
          <div class="key-blanks" id="key-blanks-${q.id}">${keyBlankSlots(q)}</div>
          <div class="key-note-row">
            <textarea class="key-explanation" data-q="${q.id}" rows="1" placeholder="讲解（可选）" autocomplete="off">${esc(q.explanation || '')}</textarea>
            <button type="button" class="btn btn-sm" data-action="key-add-blank" data-q="${q.id}">＋追加空位</button>
          </div>
        </div>`).join('')}
    </section>`;
}

export async function keyPage(chapterId) {
  // with_keys makes the API return every stored key, not just the ones the
  // learner already submitted; this page is the key editor.
  const d = await api(`api/chapters/${chapterId}/exercises?with_keys=1`);
  const status = await api('api/answers/status');
  setCourse(d.course_id);
  const courseData = await api(`api/course?course_id=${d.course_id}`);
  keySession = { chapterId: Number(chapterId), dirty: false };
  const options = courseData.chapters.map(c =>
    `<option value="${c.id}" ${c.id === Number(chapterId) ? 'selected' : ''}>第 ${c.seq} 讲 · ${esc(c.title_fr)}</option>`).join('');
  document.getElementById('app').innerHTML = `
    ${pageHead('录入答案', '逐空填入标准答案，保存后交卷即时判分；旧版本可一键重判')}
    <div class="key-topbar">
      <label>章节 <select id="key-chapter" class="filter-select">${options}</select></label>
      <span class="key-progress" id="key-progress"></span>
      <span class="spacer"></span>
      <a class="btn btn-sm" href="#/exercise/${chapterId}">回到练习页</a>
    </div>
    <div class="pending-box" style="margin-bottom:14px">
      每道题的空白按出现顺序编号（第 1 空、第 2 空…）；选项题填 <b>1,3</b> 表示第 1、3 个选项；勾选题填 <b>1</b>。
      整个题库已有 <b>${status.ready}</b> / ${status.total} 题答案。含图片的题目可点「对照原书」放大后录入。
    </div>
    <div class="key-list">${d.exercises.map(ex => keyCardHTML(ex, d.scan_base)).join('')
      || '<div class="card empty">本章暂无练习。</div>'}</div>
    <div class="submit-bar">
      <span class="info" id="key-status"></span>
      <div class="spacer"></div>
      <button type="button" class="btn btn-primary" id="key-save-all">保存本章全部答案</button>
    </div>`;
  updateKeyProgress();
  kq('#key-save-all').addEventListener('click', () => saveKeyAnswers(chapterId, true));
  kq('#key-chapter').addEventListener('change', e => {
    if (keySession?.dirty && !confirm('本章有未保存的答案，离开会丢失。继续？')) {
      e.target.value = String(chapterId);
      return;
    }
    keySession.dirty = false;
    location.hash = `#/admin/answers/${e.target.value}`;
  });
  // Bound once: re-rendering this page must not stack duplicate listeners.
  if (!inputBound) {
    inputBound = true;
    app.addEventListener('input', e => {
      if (!keySession) return;
      if (e.target.closest('.key-input, .key-explanation')) {
        keySession.dirty = true;
        updateKeyProgress();
      }
    });
  }
}

// Bound once; the delegated handler lives with the router, so reloading the
// answer-key page never stacks duplicate listeners.
export async function saveKeyExercise(exerciseId) {
  if (!keySession) return;
  await saveKeyAnswers(keySession.chapterId, false, exerciseId);
}

function updateKeyProgress() {
  const el = kq('#key-progress');
  if (!el) return;
  const inputs = kqa('.key-input');
  const filled = inputs.filter(x => x.value.trim());
  el.textContent = `${inputs.length} 个空位，已填 ${filled.length}${keySession?.dirty ? ' · 有未保存修改' : ''}`;
  const st = kq('#key-status');
  if (st) st.textContent = `本章 ${kqa('.key-q').length} 道小题 · ${inputs.length} 个空`;
}

function collectKeyAnswers(exerciseId) {
  const answers = [];
  kqa('.key-q').forEach(qEl => {
    const ex = qEl.closest('[data-key-ex]')?.dataset.keyEx;
    if (exerciseId && ex !== String(exerciseId)) return;
    const qid = Number(qEl.dataset.keyQ);
    let initial = {};
    try { initial = JSON.parse(qEl.dataset.initial || '{}'); } catch { /* ignore bad snapshot */ }
    const blanks = {};
    kqa('.key-input', qEl).forEach(inp => {
      const k = inp.dataset.blank;
      const v = keyValue(inp.value);
      if (k && v) blanks[k] = v;
    });
    const explanation = kq('.key-explanation', qEl)?.value.trim() || '';
    const had = Object.keys(initial.blanks || {}).length || initial.explanation;
    const has = Object.keys(blanks).length || explanation;
    const changed = JSON.stringify(blanks) !== JSON.stringify(initial.blanks || {})
      || explanation !== (initial.explanation || '');
    // An emptied question still has to be sent: an empty payload means
    // "remove this key", while skipping it would leave the old answer live.
    if (!has && !changed && !had) return;
    answers.push({ question_id: qid, blanks, explanation });
  });
  return { answers };
}

async function saveKeyAnswers(chapterId, whole, exerciseId = null) {
  const scope = whole ? null : exerciseId;
  const body = collectKeyAnswers(scope);
  const btn = kq('#key-save-all');
  if (whole && btn) { btn.disabled = true; btn.textContent = '正在保存…'; }
  try {
    if (!body.answers.length) throw new Error('没有可保存的答案，请至少填写一个空位或讲解');
    const r = await api('api/admin/answers', { method: 'PUT', body: JSON.stringify(body) });
    keySession.dirty = false;
    const cleared = Number(r.cleared || 0);
    toast(`已保存 ${r.updated} 题答案${cleared ? `，清空 ${cleared} 题` : ''}`);
    // Re-render this page (app.js's render() is module-private): refreshes
    // the "已有答案" pills, the global count and the saved input values.
    await keyPage(chapterId);
  } catch (err) {
    toast('保存失败：' + err.message);
  } finally {
    if (whole && btn) { btn.disabled = false; btn.textContent = '保存本章全部答案'; }
  }
}
