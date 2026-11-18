// Home-page activity visualizations: a GitHub-style heat strip per book
// (daily study time) and a daily workload check-in wall.

import { api, esc } from './ui-core.js';

const WEEKDAYS = ['一', '二', '三', '四', '五', '六', '日'];
const MONTHS = ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'];

export function fmtDuration(seconds) {
  const s = Math.max(0, Number(seconds) || 0);
  if (s === 0) return '0 分钟';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return m > 0 ? `${h} 小时 ${m} 分` : `${h} 小时`;
  if (m > 0) return `${m} 分钟`;
  return `${s} 秒`;
}

function weekdayMonFirst(day) {
  // 0=Sunday ... 6=Saturday -> 0=Monday ... 6=Sunday
  const [y, m, d] = day.split('-').map(Number);
  return (new Date(y, m - 1, d).getDay() + 6) % 7;
}

function level(value, max) {
  if (!value || value <= 0) return 0;
  if (max <= 0) return 1;
  const r = value / max;
  if (r < 0.2) return 1;
  if (r < 0.45) return 2;
  if (r < 0.75) return 3;
  return 4;
}

function monthLabels(days) {
  const marks = [];
  let last = -1;
  days.forEach((day, i) => {
    const m = Number(day.slice(5, 7)) - 1;
    if (m !== last) {
      marks.push({ index: i, label: MONTHS[m] });
      last = m;
    }
  });
  return marks;
}

// Build a week-column grid: pad the front so rows line up with weekdays.
function heatGrid(days, valueOf, tipOf, cssClass) {
  const pad = days.length ? weekdayMonFirst(days[0]) : 0;
  const cells = [];
  for (let i = 0; i < pad; i++) cells.push('<span class="heat-cell pad"></span>');
  const values = days.map(valueOf);
  const max = Math.max(0, ...values);
  days.forEach((day, i) => {
    const v = values[i];
    const lv = level(v, max);
    cells.push(`<span class="heat-cell lvl-${lv}" title="${esc(tipOf(day, v))}"></span>`);
  });
  const marks = monthLabels(days).map(m => {
    const col = Math.floor((pad + m.index) / 7);
    return `<span class="hm-mark" style="grid-column:${col + 1}">${m.label}</span>`;
  }).join('');
  return `<div class="heatmap ${cssClass}">
    <div class="hm-months">${marks}</div>
    <div class="hm-body">
      <div class="hm-days">${WEEKDAYS.map((w, i) => `<span>${i % 2 === 0 ? w : ''}</span>`).join('')}</div>
      <div class="hm-grid">${cells.join('')}</div>
    </div>
  </div>`;
}

function legend() {
  return `<div class="hm-legend"><span>少</span>
    ${[0, 1, 2, 3, 4].map(l => `<span class="heat-cell lvl-${l}"></span>`).join('')}
    <span>多</span></div>`;
}

function courseStrip(course, days) {
  const total = course.total_seconds || 0;
  const activeDays = Object.values(course.days || {}).filter(v => v > 0).length;
  const grid = heatGrid(
    days,
    d => (course.days || {})[d] || 0,
    (d, v) => `${d} · 学习 ${fmtDuration(v)}`,
    'heatmap-course',
  );
  const name = course.code && course.code.startsWith('vocabulaire')
    ? '词汇 · Vocabulaire'
    : '语法 · Grammaire';
  return `<div class="course-time">
    <div class="course-time-head">
      <div><span class="ct-name">${esc(name)}</span><span class="ct-level">${esc(course.level || '')}</span></div>
      <div class="ct-total">${fmtDuration(total)}</div>
    </div>
    ${grid}
    <div class="ct-foot">${activeDays} 天有学习记录 · 数据自部署起累计</div>
  </div>`;
}

/** Render the whole stats block into #home-stats. Safe to call repeatedly. */
export async function loadHomeStats() {
  const slot = document.getElementById('home-stats');
  if (!slot) return;
  try {
    const s = await api('api/stats/activity?days=371');
    const days = s.days || [];
    const courses = s.courses || [];
    const daily = s.daily || {};
    const wall = heatGrid(
      days,
      d => (daily[d] || {}).work || 0,
      (d) => {
        const v = daily[d] || {};
        return `${d} · 工作量 ${v.work || 0} · 学习 ${fmtDuration(v.seconds)} · 交卷 ${v.submissions || 0} 次 · 笔记 ${v.notes || 0} 条`;
      },
      'heatmap-wall',
    );
    slot.innerHTML = `
      <div class="section-label">学习时长与工作量</div>
      <div class="card stats-card">
        <div class="metrics stats-metrics">
          <div class="metric"><div class="label">累计学习</div><div class="value">${fmtDuration(s.total_seconds)}</div><div class="hint">全部课程</div></div>
          <div class="metric"><div class="label">今日学习</div><div class="value">${fmtDuration(s.today_seconds)}</div><div class="hint">${esc(s.today)}</div></div>
          <div class="metric"><div class="label">连续打卡</div><div class="value">${s.streak}</div><div class="hint">最长 ${s.longest_streak} 天</div></div>
          <div class="metric"><div class="label">活跃天数</div><div class="value">${s.active_days}</div><div class="hint">近 ${days.length} 天</div></div>
        </div>
        ${courses.map(c => courseStrip(c, days)).join('')}
      </div>
      <div class="card stats-card">
        <div class="stats-head"><h2>每日工作量 · 打卡墙</h2><span>${esc(s.work_formula || '')}</span></div>
        ${wall}
        ${legend()}
      </div>`;
  } catch (err) {
    slot.innerHTML = `<div class="card empty">学习统计加载失败：${esc(err.message)}</div>`;
  }
}
