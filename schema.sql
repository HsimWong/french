PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS course (
  id           INTEGER PRIMARY KEY,
  code         TEXT NOT NULL UNIQUE,
  title_fr     TEXT NOT NULL,
  level        TEXT NOT NULL,
  source_book  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chapter (
  id         INTEGER PRIMARY KEY,
  course_id  INTEGER NOT NULL REFERENCES course(id),
  seq        INTEGER NOT NULL,
  title_fr   TEXT NOT NULL,
  page_start INTEGER,
  page_end   INTEGER,
  pdf_start  INTEGER,
  pdf_end    INTEGER,
  status     TEXT NOT NULL DEFAULT 'pending',
  UNIQUE(course_id, seq)
);

CREATE TABLE IF NOT EXISTS chapter_section (
  id         INTEGER PRIMARY KEY,
  chapter_id INTEGER NOT NULL REFERENCES chapter(id),
  seq        INTEGER NOT NULL,
  title_fr   TEXT NOT NULL,
  page_no    INTEGER
);

CREATE TABLE IF NOT EXISTS content_block (
  id         INTEGER PRIMARY KEY,
  chapter_id INTEGER NOT NULL REFERENCES chapter(id),
  page_no    INTEGER NOT NULL,
  seq        INTEGER NOT NULL,
  type       TEXT NOT NULL,
  body_html  TEXT NOT NULL,
  source_ref TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_block_chapter ON content_block(chapter_id, page_no, seq);

CREATE TABLE IF NOT EXISTS exercise (
  id             INTEGER PRIMARY KEY,
  chapter_id     INTEGER NOT NULL REFERENCES chapter(id),
  page_no        INTEGER NOT NULL,
  seq            INTEGER NOT NULL,
  instruction_fr TEXT NOT NULL,
  label          TEXT,
  note_html      TEXT,
  source_ref     TEXT NOT NULL,
  has_image      INTEGER NOT NULL DEFAULT 0,
  scan_pages     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_exercise_chapter ON exercise(chapter_id, page_no, seq);

CREATE TABLE IF NOT EXISTS question (
  id            INTEGER PRIMARY KEY,
  exercise_id   INTEGER NOT NULL REFERENCES exercise(id),
  seq           INTEGER NOT NULL,
  type          TEXT NOT NULL,
  stem_html     TEXT NOT NULL,
  source_ref    TEXT NOT NULL,
  answer_json   TEXT,
  answer_status TEXT NOT NULL DEFAULT 'pending',
  tolerance     TEXT NOT NULL DEFAULT 'normal',
  grading_mode  TEXT NOT NULL DEFAULT 'auto',
  explanation   TEXT
);
CREATE INDEX IF NOT EXISTS idx_question_exercise ON question(exercise_id, seq);

CREATE TABLE IF NOT EXISTS attempt (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL DEFAULT 1,
  chapter_id     INTEGER REFERENCES chapter(id),
  scope          TEXT NOT NULL,
  mode           TEXT NOT NULL,
  started_at     TEXT NOT NULL,
  submitted_at   TEXT,
  score          REAL,
  total          REAL,
  grading_status TEXT NOT NULL DEFAULT 'pending',
  status         TEXT NOT NULL DEFAULT 'in_progress',
  updated_at     TEXT,
  submitted_version INTEGER,
  score_basis    TEXT,
  discarded_at   TEXT
);

CREATE TABLE IF NOT EXISTS attempt_answer (
  id               INTEGER PRIMARY KEY,
  attempt_id       INTEGER NOT NULL REFERENCES attempt(id),
  question_id      INTEGER NOT NULL REFERENCES question(id),
  user_answer_json TEXT NOT NULL,
  is_correct       INTEGER,
  score            REAL,
  feedback         TEXT,
  UNIQUE(attempt_id, question_id)
);

CREATE TABLE IF NOT EXISTS note (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL DEFAULT 1,
  chapter_id   INTEGER NOT NULL REFERENCES chapter(id),
  block_id     INTEGER REFERENCES content_block(id),
  page_no      INTEGER,
  start_offset INTEGER NOT NULL,
  end_offset   INTEGER NOT NULL,
  quote        TEXT NOT NULL,
  text         TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_note_chapter ON note(chapter_id, block_id);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- Active study time, bucketed per local day and course. The frontend reports
-- a few seconds of *active* presence (tab visible + real interaction) rather
-- than deriving duration from attempt timestamps: a draft may sit open for
-- hours, so started_at -> submitted_at is not a usable measure of effort.
CREATE TABLE IF NOT EXISTS study_log (
  user_id   INTEGER NOT NULL DEFAULT 1,
  day       TEXT NOT NULL,               -- local YYYY-MM-DD (Asia/Shanghai)
  course_id INTEGER NOT NULL REFERENCES course(id),
  seconds   INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, day, course_id)
);
CREATE INDEX IF NOT EXISTS idx_study_log_day ON study_log(user_id, day);

-- Dictionary data is independent from the course content. The reverse form
-- index maps an inflected spelling back to one or more dictionary entries.
CREATE TABLE IF NOT EXISTS dict_entry (
  id          INTEGER PRIMARY KEY,
  lemma       TEXT NOT NULL,
  display     TEXT NOT NULL,
  pos         TEXT NOT NULL,
  gloss_zh    TEXT NOT NULL,
  gloss_en    TEXT NOT NULL DEFAULT '',
  ipa         TEXT NOT NULL DEFAULT '',
  audio_url   TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT 'manual',
  license     TEXT NOT NULL DEFAULT '',
  level       TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL DEFAULT '',
  UNIQUE(lemma, pos)
);
CREATE INDEX IF NOT EXISTS idx_dict_entry_prefix ON dict_entry(lemma);

CREATE TABLE IF NOT EXISTS dict_form (
  form     TEXT NOT NULL,
  entry_id INTEGER NOT NULL REFERENCES dict_entry(id),
  feat     TEXT NOT NULL DEFAULT '',
  lemma    TEXT NOT NULL,
  source   TEXT NOT NULL DEFAULT 'manual',
  PRIMARY KEY(form, entry_id, feat)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_dict_form_lookup ON dict_form(form);

CREATE TABLE IF NOT EXISTS dict_pending (
  form             TEXT PRIMARY KEY,
  guess_lemma      TEXT NOT NULL DEFAULT '',
  reason           TEXT NOT NULL DEFAULT '',
  hits             INTEGER NOT NULL DEFAULT 1,
  first_seen       TEXT NOT NULL,
  last_seen        TEXT NOT NULL,
  resolved_entry_id INTEGER REFERENCES dict_entry(id)
);

INSERT OR IGNORE INTO meta(key, value) VALUES ('answer_import_hint', 'pending');
