#!/usr/bin/env python3
"""Import answer keys (from the scanned Corrigés) into the question table.

Source-of-truth files live in data/import/answers/gNN.json (grammar) shaped like:

  {
    "pages": {
      "9": {                      # page_no (main book page, as in "Exercices p. 9")
        "1": {                    # exercise order within that page (circled ① -> "1")
          "2": "Je suis de Madrid.",   # question seq -> single blank answer
          "3": ["blanc1", "blanc2"]    # multi-blank -> one string per blank
        }
      }
    }
  }

Each answer string may hold variants joined by "|", e.g. "Je suis de Madrid|Je viens de Madrid".

The importer resolves each entry to the real question row by
(chapter seq -> chapter.id, page_no, exercise order on page, question seq),
verifies the number of supplied blanks equals the number of {{b/L/T/c}} placeholders
in the stem, then writes answer_json / answer_status='ready'.

Re-seeding never overwrites answer_json (ON CONFLICT keeps it), so imported
answers survive restarts and dev re-seeds. Old frozen attempts are re-scored by
the Go server on demand.

Usage:
  python tools/import_answers.py                # all data/import/answers/g*.json
  python tools/import_answers.py g02.json g03.json
"""
import glob
import json
import os
import re
import sqlite3
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "french.db")
ANSWERS_DIR = os.path.join(ROOT, "data", "import", "answers")

PLACEHOLDER = re.compile(r"\{\{([bLTco])(!?)(?::([^}]*))?\}\}")


def blank_count(stem):
    return len(PLACEHOLDER.findall(stem or ""))


def to_variants(text):
    """'a|b' -> ["a","b"] (trimmed, empties dropped)."""
    out = []
    for v in str(text).split("|"):
        v = v.strip()
        if v:
            out.append(v)
    return out


def answer_json_for(blanks):
    obj = {}
    for i, b in enumerate(blanks, 1):
        obj[str(i)] = to_variants(b)
    return json.dumps(obj, ensure_ascii=False)


def import_file(cur, path):
    base = os.path.basename(path)
    m = re.match(r"g(\d{2})\.json$", base)
    if not m:
        return 0, 0, 0
    seq = int(m.group(1))
    row = cur.execute(
        "SELECT id FROM chapter WHERE course_id=1 AND seq=?", (seq,)
    ).fetchone()
    if not row:
        print("  %s: no grammar chapter seq %d" % (base, seq))
        return 0, 0, 1
    cid = row[0]
    data = json.load(open(path, encoding="utf-8"))
    pages = data.get("pages", {})

    written = mism = 0
    for page_str, exmap in pages.items():
        page = int(page_str)
        exs = cur.execute(
            "SELECT id FROM exercise WHERE chapter_id=? AND page_no=? ORDER BY seq",
            (cid, page),
        ).fetchall()
        exids = [r[0] for r in exs]
        for exord, qmap in exmap.items():
            i = int(exord) - 1
            if i < 0 or i >= len(exids):
                print("  %s p%d ex%s: no exercise at this position (page has %d)"
                      % (base, page, exord, len(exids)))
                mism += 1
                continue
            eid = exids[i]
            for qseq_str, spec in qmap.items():
                qseq = int(qseq_str)
                qrow = cur.execute(
                    "SELECT id, stem_html FROM question WHERE exercise_id=? AND seq=?",
                    (eid, qseq),
                ).fetchone()
                if not qrow:
                    print("  %s p%d ex%s q%d: no question"
                          % (base, page, exord, qseq))
                    mism += 1
                    continue
                qid, stem = qrow
                nb = blank_count(stem)
                blanks = spec if isinstance(spec, list) else [spec]
                if len(blanks) != nb:
                    print("  %s p%d ex%s q%d: provided %d blanks != stem %d, SKIP"
                          % (base, page, exord, qseq, len(blanks), nb))
                    mism += 1
                    continue
                cur.execute(
                    "UPDATE question SET answer_json=?, answer_status='ready' WHERE id=?",
                    (answer_json_for(blanks), qid),
                )
                written += 1
    return written, mism, 0


def main():
    con = sqlite3.connect(DB, timeout=30)
    con.execute("PRAGMA busy_timeout=30000")
    cur = con.cursor()
    if len(sys.argv) > 1:
        files = [
            p if os.path.isabs(p) else os.path.join(ANSWERS_DIR, p)
            for p in sys.argv[1:]
        ]
    else:
        files = sorted(glob.glob(os.path.join(ANSWERS_DIR, "g*.json")))

    tw = tm = tn = 0
    for f in files:
        w, mi, no = import_file(cur, f)
        con.commit()
        if w or mi or no:
            print("%s: written %d, skipped/mismatch %d" % (os.path.basename(f), w, mi))
        tw += w
        tm += mi
        tn += no

    ready = cur.execute(
        "SELECT count(*) FROM question WHERE answer_status='ready'"
    ).fetchone()[0]
    total = cur.execute(
        """SELECT count(*) FROM question q
           JOIN exercise e ON e.id=q.exercise_id
           JOIN chapter c ON c.id=e.chapter_id WHERE c.course_id=1"""
    ).fetchone()[0]
    print("TOTAL: written %d, skipped/mismatch %d, no-chapter %d" % (tw, tm, tn))
    print("grammar answers ready: %d / %d questions" % (ready, total))
    con.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
