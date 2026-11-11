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
  source_ref     TEXT NOT NULL
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
  status         TEXT NOT NULL DEFAULT 'in_progress'
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

INSERT OR IGNORE INTO meta(key, value) VALUES ('answer_import_hint', 'pending');
