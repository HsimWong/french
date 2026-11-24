// Camera-speaking practice: past prompts, revision history and video uploads.
// Kept in its own module so app.js only needs one import and one route branch.
import { api, esc, pageHead, toast } from './ui-core.js';

const KIND_LABELS = {
  draft: '我的译文',
  correction: '批改意见',
  final: '定稿法语',
};

function when(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return esc(iso);
  return d.toLocaleString('zh-CN', { hour12: false });
}

function humanSize(n) {
  const bytes = Number(n) || 0;
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function revisionHTML(rev) {
  const label = KIND_LABELS[rev.kind] || rev.kind;
  return `<div class="speaking-rev">
    <div class="speaking-rev-head">
      <span class="speaking-kind kind-${esc(rev.kind)}">v${rev.version} · ${esc(label)}</span>
      <span class="speaking-time">${when(rev.created_at)}</span>
    </div>
    <div class="speaking-text">${esc(rev.text)}</div>
    ${rev.note ? `<div class="speaking-note">${esc(rev.note)}</div>` : ''}
  </div>`;
}

function videoHTML(v) {
  return `<div class="speaking-video">
    <video controls preload="metadata" playsinline src="${esc(v.url)}"></video>
    <div class="speaking-video-meta">
      <span>${esc(v.original_name)}</span>
      <span>${humanSize(v.size)} · ${when(v.created_at)}</span>
    </div>
  </div>`;
}

function practiceHTML(p) {
  const revs = p.revisions.length
    ? p.revisions.map(revisionHTML).join('')
    : '<div class="speaking-empty">还没有提交记录。先写你的法语译文，再提交修改。</div>';
  const videos = p.videos.length
    ? p.videos.map(videoHTML).join('')
    : '<div class="speaking-empty">还没有上传视频。</div>';
  return `<section class="card speaking-card" data-practice="${p.id}">
    <div class="speaking-card-head">
      <div>
        <div class="speaking-kicker">第 ${p.episode} 期 · ${esc(p.date)} · ${esc(p.level)}</div>
        <h2>${esc(p.title)}</h2>
        ${p.focus ? `<div class="speaking-focus">${esc(p.focus)}</div>` : ''}
      </div>
      <div class="speaking-counts">
        <span>${p.revisions.length} 条修改</span>
        <span>${p.videos.length} 个视频</span>
      </div>
    </div>
    <details class="speaking-source">
      <summary>查看中文原稿</summary>
      <div class="speaking-text">${esc(p.source)}</div>
    </details>
    <div class="speaking-grid">
      <div class="speaking-pane">
        <div class="speaking-pane-head">
          <h3>往期提交记录</h3>
          <span>最新版本在前</span>
        </div>
        <div class="speaking-revs">${revs}</div>
        <div class="speaking-form">
          <select data-speaking-kind aria-label="提交类型">
            <option value="draft">我的译文</option>
            <option value="correction">批改意见</option>
            <option value="final">定稿法语</option>
          </select>
          <textarea data-speaking-text rows="5" placeholder="写下你的法语版本 / 批改内容…"></textarea>
          <input type="text" data-speaking-note placeholder="备注（可选）：例如本次重点、老师建议">
          <button type="button" class="btn btn-primary" data-speaking-submit="${p.id}">提交修改</button>
        </div>
      </div>
      <div class="speaking-pane">
        <div class="speaking-pane-head">
          <h3>镜头视频</h3>
          <span>MP4 / WebM · 最大 60 MB</span>
        </div>
        <div class="speaking-videos">${videos}</div>
        <div class="speaking-form">
          <input type="file" data-speaking-file="${p.id}" accept="video/mp4,video/webm,.mp4,.webm,.m4v">
          <button type="button" class="btn" data-speaking-upload="${p.id}">上传视频</button>
        </div>
      </div>
    </div>
  </section>`;
}

function bind(app) {
  app.querySelectorAll('[data-speaking-submit]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const card = btn.closest('[data-practice]');
      const text = card.querySelector('[data-speaking-text]').value.trim();
      if (!text) {
        toast('请先填写法语内容');
        return;
      }
      const kind = card.querySelector('[data-speaking-kind]').value;
      const note = card.querySelector('[data-speaking-note]').value.trim();
      btn.disabled = true;
      try {
        await api(`api/speaking/practices/${btn.dataset.speakingSubmit}/revisions`, {
          method: 'POST',
          body: JSON.stringify({ kind, text, note }),
        });
        toast('修改已提交，历史版本已保留');
        await viewSpeaking(app);
      } catch (err) {
        toast(`提交失败：${err.message}`);
        btn.disabled = false;
      }
    });
  });

  app.querySelectorAll('[data-speaking-upload]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const card = btn.closest('[data-practice]');
      const input = card.querySelector('[data-speaking-file]');
      const file = input.files && input.files[0];
      if (!file) {
        toast('请先选择视频文件');
        return;
      }
      if (file.size > 60 * 1024 * 1024) {
        toast('视频不能超过 60 MB');
        return;
      }
      const fd = new FormData();
      fd.append('video', file, file.name);
      btn.disabled = true;
      btn.textContent = '上传中…';
      try {
        // The shared api() sets a JSON header by default; clear it so the
        // browser can add the multipart boundary.
        await api(`api/speaking/practices/${btn.dataset.speakingUpload}/videos`, {
          method: 'POST',
          body: fd,
          headers: {},
        });
        toast('视频已上传');
        await viewSpeaking(app);
      } catch (err) {
        toast(`上传失败：${err.message}`);
        btn.disabled = false;
        btn.textContent = '上传视频';
      }
    });
  });
}

export async function viewSpeaking(app) {
  const data = await api('api/speaking/practices');
  const practices = data.practices || [];
  const revisions = practices.reduce((n, p) => n + (p.revisions || []).length, 0);
  const videos = practices.reduce((n, p) => n + (p.videos || []).length, 0);
  app.innerHTML = `
    ${pageHead('镜头前法语练习', '每日稿件 · 往期提交记录 · 修改版本 · 镜头视频')}
    <div class="metrics">
      <div class="metric"><div class="label">练习期数</div><div class="value">${practices.length}</div><div class="hint">每日自动化发布</div></div>
      <div class="metric"><div class="label">修改记录</div><div class="value">${revisions}</div><div class="hint">每次提交保留一个版本</div></div>
      <div class="metric"><div class="label">视频</div><div class="value">${videos}</div><div class="hint">镜头练习录像</div></div>
      <div class="metric"><div class="label">水平区间</div><div class="value">A2–B1</div><div class="hint">主题与语法逐期轮换</div></div>
    </div>
    ${practices.length
      ? practices.map(practiceHTML).join('')
      : '<div class="card empty">还没有练习记录。每日定时任务生成新稿件后会自动发布到这里。</div>'}`;
  bind(app);
}
