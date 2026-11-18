// Active study-time tracker.
//
// Counts only time that is genuinely spent studying: the tab must be visible,
// the user must have interacted within IDLE_MS, and the current route must be
// a learning page. Accumulated seconds are flushed to the server in small
// batches, keyed by course so switching books mid-session stays accurate.

import { api, state } from './ui-core.js';

const TICK_MS = 5000;       // check every 5 s
const IDLE_MS = 60_000;     // stop counting after 1 min without interaction
const FLUSH_AT = 20;        // send when a course has 20+ pending seconds
const MAX_TICK = 8;         // ignore long timer suspensions (sleep / background)

// Everything that is actual study work: reading, exercises, reviewing a
// submitted attempt, revision pages, and looking words up. The old list only
// covered the reading pages, so time spent on the answer sheet was lost.
const LEARNING = /^\/(chapter|exercise|course|chapters|notes|attempt|result|revision|dictionary)(\/|$)/;

let lastActivity = Date.now();
let lastTick = Date.now();
let pending = new Map();   // courseId -> seconds
let sending = false;

for (const ev of ['pointerdown', 'keydown', 'scroll', 'mousemove', 'touchstart']) {
  window.addEventListener(ev, () => { lastActivity = Date.now(); }, { passive: true });
}
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') lastTick = Date.now();
  else flush(true);
});
window.addEventListener('pagehide', () => flush(true, true));

function currentCourse() {
  let id = Number(state.courseId);
  if (!Number.isFinite(id) || id <= 0) {
    id = Number(sessionStorage.getItem('fp.courseId'));
  }
  return Number.isFinite(id) && id > 0 ? id : 0;
}

function isLearningRoute() {
  const raw = location.hash.slice(1) || '/';
  const path = raw.split('?')[0] || '/';
  return LEARNING.test(path);
}

function add(courseId, seconds) {
  // The API takes an integer number of seconds. A fractional value makes
  // encoding/json reject the whole request with 400, which is what silently
  // threw away every heartbeat before.
  seconds = Math.round(seconds);
  if (!courseId || seconds <= 0) return;
  pending.set(courseId, (pending.get(courseId) || 0) + seconds);
}

async function flush(force = false, useBeacon = false) {
  if (sending || pending.size === 0) return;
  const batch = [];
  for (const [courseId, seconds] of pending) {
    if (force || seconds >= FLUSH_AT) batch.push({ course_id: courseId, seconds });
  }
  if (!batch.length) return;
  for (const item of batch) pending.delete(item.course_id);

  if (useBeacon && navigator.sendBeacon) {
    // Beacon survives page unload; fetch may be cancelled mid-flight. The
    // endpoint accepts one record per call, so send a beacon for each course.
    for (const item of batch) {
      navigator.sendBeacon('api/study/ping',
        new Blob([JSON.stringify(item)], { type: 'application/json' }));
    }
    return;
  }

  sending = true;
  for (const item of batch) {
    try {
      await api('api/study/ping', {
        method: 'POST',
        body: JSON.stringify(item),
      });
    } catch (_) {
      // Put it back if the request failed for a reason other than auth; the
      // next tick will retry. A 401 is handled by api() (redirect to login).
      if (!force) add(item.course_id, item.seconds);
    }
  }
  sending = false;
  if (force && pending.size) flush(true);
}

function tick() {
  const now = Date.now();
  const elapsed = Math.min((now - lastTick) / 1000, MAX_TICK);
  lastTick = now;
  if (
    document.visibilityState === 'visible' &&
    isLearningRoute() &&
    now - lastActivity <= IDLE_MS
  ) {
    add(currentCourse(), Math.max(0, elapsed));
  }
  flush();
}

window.setInterval(tick, TICK_MS);
