// Free-form per-chapter summary: markdown text + images stored through the
// shared note_image BLOB endpoint. Deliberately separate from selection notes.
import { api, esc, toast } from './ui-core.js';
import { bindImagePicker } from './note-images.js';
import { renderMarkdown } from './markdown.js';

export function summaryEditorHTML() {
  return `<section class="chapter-summary card" id="chapter-summary">
    <div class="sum-head">
      <div>
        <div class="sum-title">本章总结笔记</div>
        <div class="sum-sub">支持 Markdown 与图片 · 独立保存，不属于选中文本笔记</div>
      </div>
      <span class="sum-state" id="sum-state">未保存</span>
    </div>
    <div class="sum-grid">
      <div class="sum-edit">
        <textarea class="sum-input" rows="10" placeholder="# 本章重点&#10;- 要点一&#10;- 要点二&#10;&#10;支持 **加粗**、*斜体*、\`代码\`、列表、链接和图片。"></textarea>
        <div class="drawer-images sum-images"></div>
        <input type="file" class="hidden sum-image-file" accept="image/*">
        <div class="popover-actions">
          <button type="button" class="btn btn-sm sum-add-image" title="插入截图（自动压缩）">＋ 图片</button>
          <button type="button" class="btn btn-primary sum-save">保存总结</button>
        </div>
      </div>
      <div class="sum-preview">
        <div class="sum-preview-label">Markdown 预览</div>
        <div class="sum-preview-body md-body"></div>
      </div>
    </div>
  </section>`;
}

export function initSummaryEditor(chapterId, root, initialText = '') {
  if (!root) return;
  const textarea = root.querySelector('.sum-input');
  const preview = root.querySelector('.sum-preview-body');
  const stateEl = root.querySelector('.sum-state');
  const saveBtn = root.querySelector('.sum-save');
  if (!textarea || !saveBtn) return;
  textarea.value = initialText || '';
  const refresh = () => {
    preview.innerHTML = renderMarkdown(textarea.value) || '<span class="sum-placeholder">写下总结后，这里实时预览。</span>';
  };
  bindImagePicker({
    textarea,
    button: root.querySelector('.sum-add-image'),
    input: root.querySelector('.sum-image-file'),
    preview: root.querySelector('.sum-images'),
    onChange: () => {
      refresh();
      if (stateEl) stateEl.textContent = '未保存';
    },
  });
  textarea.addEventListener('input', () => {
    refresh();
    if (stateEl) stateEl.textContent = '未保存';
  });
  refresh();
  if (stateEl && textarea.value) stateEl.textContent = '已保存';
  saveBtn.addEventListener('click', async () => {
    saveBtn.disabled = true;
    try {
      await api(`api/chapters/${chapterId}/summary`, {
        method: 'PUT', body: JSON.stringify({ text: textarea.value }),
      });
      if (stateEl) stateEl.textContent = textarea.value ? '已保存' : '已清空';
      toast('章节总结已保存');
    } catch (err) {
      toast('保存失败：' + err.message);
    } finally {
      saveBtn.disabled = false;
    }
  });
}

export function summaryCardHTML(s) {
  const id = s.chapter_id;
  return `<div class="note-item summary-item" data-summary="${id}">
    <div class="sum-card-head">
      <span class="sum-card-badge">章节总结</span>
      <span class="note-meta">${s.course_id === 2 ? '词汇' : '语法'} · ${esc(s.chapter_title)} · ${esc((s.updated_at || '').slice(0, 16).replace('T', ' '))}</span>
      <span class="note-actions">
        <a class="btn btn-sm" href="#/chapter/${id}?summary=1">跳转章节</a>
        <button class="btn btn-sm" data-action="summary-edit" data-id="${id}">编辑</button>
      </span>
    </div>
    <div class="sum-card-body md-body" id="summary-body-${id}">${renderMarkdown(s.text)}</div>
    <div class="note-editor hidden" id="summary-edit-${id}">
      <textarea rows="6" id="summary-edit-input-${id}">${esc(s.text)}</textarea>
      <div class="note-editor-actions">
        <button class="btn btn-primary btn-sm" data-action="summary-save" data-id="${id}">保存</button>
        <button class="btn btn-sm" data-action="summary-cancel" data-id="${id}">取消</button>
      </div>
    </div>
  </div>`;
}

export function bindSummaryList(root) {
  if (!root) return;
  root.addEventListener('click', async e => {
    const edit = e.target.closest('[data-action="summary-edit"]');
    if (edit) {
      root.querySelector(`#summary-edit-${edit.dataset.id}`)?.classList.remove('hidden');
      return;
    }
    const cancel = e.target.closest('[data-action="summary-cancel"]');
    if (cancel) {
      root.querySelector(`#summary-edit-${cancel.dataset.id}`)?.classList.add('hidden');
      return;
    }
    const save = e.target.closest('[data-action="summary-save"]');
    if (!save) return;
    const id = save.dataset.id;
    const text = root.querySelector(`#summary-edit-input-${id}`)?.value ?? '';
    save.disabled = true;
    try {
      await api(`api/chapters/${id}/summary`, { method: 'PUT', body: JSON.stringify({ text }) });
      const body = root.querySelector(`#summary-body-${id}`);
      if (body) body.innerHTML = renderMarkdown(text) || '<span class="sum-placeholder">（空）</span>';
      root.querySelector(`#summary-edit-${id}`)?.classList.add('hidden');
      toast('章节总结已保存');
    } catch (err) {
      toast('保存失败：' + err.message);
    } finally {
      save.disabled = false;
    }
  });
}
