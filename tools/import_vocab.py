#!/usr/bin/env python3
"""Import the vocabulary answer keys (data/import/answers_vocab/vNN.json) into french.db.

The vocabulary keys are *sparse* answer objects: only blanks with a meaningful
value are present.  This is deliberate for {{c:g}} tick groups (the unticked
box must not become a blank that can never be scored) and for V/F keys without
a correction.  Example:

  {"pages": {"7": {"1": {"1": {"1": ["1"], "3": ["vous"]}}}}}
                     |   |     |     blank 1 = tick "Vrai" = "1"
                     |   |     |     blank 3 = correction text
                     |   |     question seq 1
                     |   1st exercise on page 7
                     main-book page 7

Before writing, every key is checked against the real stem: the index must be
inside the placeholder list, a {{c}} blank must hold "1", an {{o}} blank must
hold an option index, and all variants must be non-empty strings.

Usage:
  python tools/import_vocab.py                # all vNN.json
  python tools/import_vocab.py v05.json
"""
import glob
import json
import os
import re
import sqlite3
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "french.db")
DIR = os.path.join(ROOT, "data", "import", "answers_vocab")
PH = re.compile(r"\{\{([bLTco])(!?)(?::([^}]*))?\}\}")


def check(obj, stem):
    ph = PH.findall(stem or "")
    if not isinstance(obj, dict) or not obj:
        return "not a non-empty object"
    if any(k == "explanation" for k in obj):
        return "reserved key"
    for k, vals in obj.items():
        if not str(k).isdigit() or not (1 <= int(k) <= len(ph)):
            return "key %r outside 1..%d" % (k, len(ph))
        kind = ph[int(k) - 1][0]
        if not isinstance(vals, list) or not vals:
            return "key %s: expected a variant list" % k
        for v in vals:
            if not isinstance(v, str) or not v.strip():
                return "key %s: empty variant" % k
            if kind == "c" and v != "1":
                return "key %s: {{c}} must be \"1\", got %r" % (k, v)
            if kind == "o" and not re.fullmatch(r"\d+(,\d+)*", v):
                return "key %s: {{o}} must be an option index, got %r" % (k, v)
    return None


def import_file(cur, path):
    base = os.path.basename(path)
    m = re.match(r"v(\d{2})\.json$", base)
    if not m:
        return 0, 0
    seq = int(m.group(1))
    row = cur.execute("SELECT id FROM chapter WHERE course_id=2 AND seq=?", (seq,)).fetchone()
    if not row:
        print("  %s: no vocabulary chapter seq %d" % (base, seq))
        return 0, 1
    cid = row[0]
    pages = json.load(open(path, encoding="utf-8")).get("pages", {})
    # Manual overrides (e.g. crossword figures an automated pass cannot read)
    # live in over/vNN.json with the same shape and win over the generated key.
    over_path = os.path.join(DIR, "over", base)
    if os.path.exists(over_path):
        over = json.load(open(over_path, encoding="utf-8")).get("pages", {})
        for pg, exs in over.items():
            for ex, qm in exs.items():
                for qseq, obj in qm.items():
                    pages.setdefault(pg, {}).setdefault(ex, {})[qseq] = obj
        print("  %s: merged %d override(s) from over/%s"
              % (base, sum(len(qm) for exs in over.values() for qm in exs.values()), base))
    written = skipped = 0
    for page_str, exmap in pages.items():
        page = int(page_str)
        exids = [r[0] for r in cur.execute(
            "SELECT id FROM exercise WHERE chapter_id=? AND page_no=? ORDER BY seq",
            (cid, page)).fetchall()]
        for exord, qmap in exmap.items():
            i = int(exord) - 1
            if i < 0 or i >= len(exids):
                print("  %s p%d ex%s: page has only %d exercises"
                      % (base, page, exord, len(exids)))
                skipped += len(qmap)
                continue
            eid = exids[i]
            for qseq_str, obj in qmap.items():
                q = cur.execute(
                    "SELECT id, stem_html FROM question WHERE exercise_id=? AND seq=?",
                    (eid, int(qseq_str))).fetchone()
                if not q:
                    print("  %s p%d ex%s q%s: no such question" % (base, page, exord, qseq_str))
                    skipped += 1
                    continue
                err = check(obj, q[1])
                if err:
                    print("  %s p%d ex%s q%s: %s, SKIP" % (base, page, exord, qseq_str, err))
                    skipped += 1
                    continue
                cur.execute("UPDATE question SET answer_json=?, answer_status='ready' WHERE id=?",
                            (json.dumps(obj, ensure_ascii=False), q[0]))
                written += 1
    return written, skipped


def main():
    con = sqlite3.connect(DB, timeout=30)
    con.execute("PRAGMA busy_timeout=30000")
    cur = con.cursor()
    files = ([p if os.path.isabs(p) else os.path.join(DIR, p) for p in sys.argv[1:]]
             if len(sys.argv) > 1 else sorted(glob.glob(os.path.join(DIR, "v*.json"))))
    tw = ts = 0
    for f in files:
        w, s = import_file(cur, f)
        con.commit()
        if w or s:
            print("%s: written %d, skipped %d" % (os.path.basename(f), w, s))
        tw += w
        ts += s
    ready = cur.execute("""SELECT count(*) FROM question q
        JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id
        WHERE c.course_id=2 AND q.answer_status='ready'""").fetchone()[0]
    total = cur.execute("""SELECT count(*) FROM question q
        JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id
        WHERE c.course_id=2""").fetchone()[0]
    print("TOTAL: written %d, skipped %d" % (tw, ts))
    print("vocabulary answers ready: %d / %d questions" % (ready, total))
    con.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
