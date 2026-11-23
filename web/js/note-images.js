// Images in notes. Text keeps a plain markdown-style reference such as
// ![图片](api/note-images/12) so notes stay searchable and portable; only the
// bytes live in SQLite. This module has no dependency on app.js.
import { api, esc, toast } from './ui-core.js';

const IMAGE_REF = /!\[([^\]]*)\]\((\/?api\/note-images\/\d+)\)/g;

export function imageRefs(text) {
  const out = [];
  for (const m of String(text || '').matchAll(IMAGE_REF)) out.push({ alt: m[1], url: m[2] });
  return out;
}

function oneLine(s) {
  return esc(s).replace(/\s*\r?\n\s*/g, ' ');
}

// Compact rendering for the notes list: text on one line, at most two thumbs.
export function renderNoteText(text, maxImages = 2) {
  const src = String(text || '');
  let html = '', last = 0, shown = 0;
  IMAGE_REF.lastIndex = 0;
  for (const m of src.matchAll(IMAGE_REF)) {
    html += oneLine(src.slice(last, m.index));
    if (shown < maxImages) {
      html += `<img class="note-inline-img" src="${esc(m[2])}" alt="${esc(m[1] || '笔记图片')}" loading="lazy">`;
    } else if (shown === maxImages) {
      html += `<span class="note-img-more" title="更多图片">+图</span>`;
    }
    shown++;
    last = m.index + m[0].length;
  }
  html += oneLine(src.slice(last));
  return html;
}

export function renderImagePreviews(text) {
  const refs = imageRefs(text);
  if (!refs.length) return '<span class="drawer-images-empty">还没有图片。点「添加图片」插入截图。</span>';
  return refs.map(r => `<span class="drawer-thumb" title="点击图片查看大图">
    <img src="${esc(r.url)}" alt="${esc(r.alt || '笔记图片')}" loading="lazy">
    <button type="button" data-remove-image="${esc(r.url)}" aria-label="移除这张图片">×</button>
  </span>`).join('');
}

export function insertImageRef(textarea, url) {
  const ref = `![图片](${url})`;
  const start = textarea.selectionStart ?? textarea.value.length;
  const end = textarea.selectionEnd ?? start;
  textarea.value = textarea.value.slice(0, start) + ref + textarea.value.slice(end);
  textarea.setSelectionRange(start + ref.length, start + ref.length);
  textarea.focus();
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}

export function removeImageRef(textarea, url) {
  const escaped = url.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`\\n?!\\[[^\\]]*\\]\\(${escaped}\\)`, 'g');
  textarea.value = textarea.value.replace(re, '');
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}

async function downscale(file) {
  let w, h, draw, cleanup = () => {};
  if (typeof createImageBitmap === 'function') {
    const bmp = await createImageBitmap(file);
    w = bmp.width; h = bmp.height;
    draw = ctx => ctx.drawImage(bmp, 0, 0, ctx.canvas.width, ctx.canvas.height);
    cleanup = () => bmp.close?.();
  } else {
    const url = URL.createObjectURL(file);
    const img = new Image();
    await new Promise((resolve, reject) => {
      img.onload = resolve;
      img.onerror = () => reject(new Error('图片无法读取'));
      img.src = url;
    });
    w = img.naturalWidth; h = img.naturalHeight;
    draw = ctx => ctx.drawImage(img, 0, 0, ctx.canvas.width, ctx.canvas.height);
    cleanup = () => URL.revokeObjectURL(url);
  }
  try {
    const max = 1400;
    const scale = Math.min(1, max / Math.max(w, h));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(w * scale));
    canvas.height = Math.max(1, Math.round(h * scale));
    const ctx = canvas.getContext('2d');
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    draw(ctx);
    return canvas.toDataURL('image/jpeg', 0.82);
  } finally {
    cleanup();
  }
}

export async function uploadNoteImage(file) {
  if (!file || !/^image\//.test(file.type)) throw new Error('请选择图片文件');
  const data = await downscale(file);
  const d = await api('api/note-images', { method: 'POST', body: JSON.stringify({ data }) });
  return d.url;
}

export function bindImagePicker({ textarea, button, input, preview, onChange = () => {} }) {
  if (!textarea || !button || !input) return () => {};
  const refresh = () => { if (preview) preview.innerHTML = renderImagePreviews(textarea.value); };
  button.addEventListener('click', () => input.click());
  input.addEventListener('change', async () => {
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    button.disabled = true;
    try {
      insertImageRef(textarea, await uploadNoteImage(file));
      refresh();
      onChange();
      toast('图片已插入，保存笔记后生效');
    } catch (err) {
      toast('图片上传失败：' + err.message);
    } finally {
      button.disabled = false;
    }
  });
  preview?.addEventListener('click', e => {
    const btn = e.target.closest('[data-remove-image]');
    if (!btn) return;
    removeImageRef(textarea, btn.dataset.removeImage);
    refresh();
    onChange();
  });
  textarea.addEventListener('input', refresh);
  refresh();
  return refresh;
}

/* Click any note thumbnail to see the full image. */
let lightbox;
function closeLightbox() { lightbox?.classList.add('hidden'); }
document.addEventListener('click', e => {
  const img = e.target.closest('.note-inline-img, .drawer-thumb img');
  if (!img) return;
  if (!lightbox) {
    lightbox = document.createElement('div');
    lightbox.className = 'note-lightbox hidden';
    lightbox.innerHTML = '<button type="button" class="note-lightbox-close" aria-label="关闭">×</button><img alt="笔记图片">';
    document.body.appendChild(lightbox);
    lightbox.addEventListener('click', ev => {
      if (ev.target === lightbox || ev.target.closest('.note-lightbox-close')) closeLightbox();
    });
    document.addEventListener('keydown', ev => { if (ev.key === 'Escape') closeLightbox(); });
  }
  lightbox.querySelector('img').src = img.src;
  lightbox.classList.remove('hidden');
});
