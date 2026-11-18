// Small, dependency-free helpers shared by the app shell and its subpages.
// Keeping them in their own module avoids the circular import that a page
// would create by reaching back into app.js for api(), esc() and toast().

let authRedirecting = false;

export async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401 && data.auth_required) {
    // Session expired: a page reload reaches the self-contained login screen
    // at the same URL, so this works under any mount prefix.
    if (!authRedirecting) {
      authRedirecting = true;
      location.reload();
    }
    throw new Error(data.error || '需要访问口令');
  }
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// Set by app.js once the reader renders; dictionary.js reads it lazily.
export const ui = { app: null };

// One session object shared by the app shell, the dictionary drawer and the
// answer-key page. It is initialised here (not in app.js) so that any module
// can import it without depending on evaluation order.
export const state = (window.__fpState ||= {
  chapter: null,
  blocksById: {},
  pendingSelection: null,
  editingNoteId: null,
  noteQuery: '',
  noteCourse: '',
  courseId: Number(sessionStorage.getItem('fp.courseId') || 1),
  courses: null,
  insideReader: false,
  exercisePage: null,
});

export function esc(s) {
  const map = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  return String(s ?? '').replace(/[&<>"']/g, c => map[c]);
}

let toastTimer;
export function toast(msg) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('hidden'), 2600);
}

export function pageHead(title, sub) {
  return `<div class="page-head"><h1>${esc(title)}</h1><p>${esc(sub)}</p></div>`;
}

// Course names are refreshed by the shell; subpages only need reads.
export function setCourse(id) {
  state.courseId = Number(id) || 1;
  sessionStorage.setItem('fp.courseId', String(state.courseId));
}

// The original-page toggle used by reading, exercises and answer entry.
export function scanBlock(scanBase, pageNo) {
  if (!scanBase) return '';
  return `<div class="scan-wrap">
    <button type="button" class="btn btn-sm scan-toggle" data-scan="${pageNo}">对照原书 p. ${pageNo} ▾</button>
    <div class="scan-frame hidden" data-scan-frame="${pageNo}">
      <img src="${scanBase}${pageNo}.jpg" alt="原书印刷第 ${pageNo} 页影印" loading="lazy">
    </div>
  </div>`;
}
