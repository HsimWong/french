#!/usr/bin/env python3
"""Import the 简明法语教程 (第3版上册) course from data/import/jianming/jianming.json.

New course (id=3, code=jianming_3e_upper) with 8 chapters: Leçon 25-30,
EXERCICES DE RÉVISION (its own chapter), and TABLEAU DE CONJUGAISON (appendix).

ID bands are chosen well clear of the two existing courses:
  course 3
  chapter          2001..2008
  chapter_section  20_001_000 + seq*100 + n
  content_block    20_100_000 + seq*10_000 + n
  exercise         21_000_000 + seq*10_000 + n
  question         22_000_000 + seq*10_000 + m

Answers are NOT imported: answer_json stays NULL and answer_status='pending',
so the online exercises can be attempted but are not auto-graded.

Idempotent: a second run deletes only this course's rows first (cascade via
explicit DELETEs) and re-inserts from the JSON.  Other courses are untouched.
"""
import json
import os
import sqlite3

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "french.db")
SRC = os.path.join(ROOT, "data", "import", "jianming", "jianming.json")

COURSE_ID = 3
CH_BASE = 2000          # chapter ids = CH_BASE + seq
SEC_BASE = 20_001_000
BLOCK_BASE = 20_100_000
EX_BASE = 21_000_000
Q_BASE = 22_000_000


def main():
    with open(SRC, encoding="utf-8") as f:
        data = json.load(f)
    course = data["course"]
    chapters = data["chapters"]

    con = sqlite3.connect(DB, timeout=15)
    cur = con.cursor()
    cur.execute("PRAGMA foreign_keys=ON")

    # ---- idempotent purge of ONLY this course ----
    ch_ids = [CH_BASE + c["seq"] for c in chapters]
    if ch_ids:
        ph = ",".join("?" * len(ch_ids))
        cur.execute("DELETE FROM question WHERE exercise_id IN "
                    "(SELECT id FROM exercise WHERE chapter_id IN (%s))" % ph, ch_ids)
        cur.execute("DELETE FROM exercise WHERE chapter_id IN (%s)" % ph, ch_ids)
        cur.execute("DELETE FROM content_block WHERE chapter_id IN (%s)" % ph, ch_ids)
        cur.execute("DELETE FROM chapter_section WHERE chapter_id IN (%s)" % ph, ch_ids)
        cur.execute("DELETE FROM note WHERE chapter_id IN (%s)" % ph, ch_ids)
        cur.execute("DELETE FROM chapter WHERE id IN (%s)" % ph, ch_ids)
    cur.execute("DELETE FROM course WHERE id=?", (COURSE_ID,))

    # ---- course (level deliberately empty: no A2/B1 label) ----
    cur.execute(
        "INSERT INTO course(id,code,title_fr,level,source_book) VALUES(?,?,?,?,?)",
        (COURSE_ID, course["code"], course["title_fr"], course["level"],
         course["source_book"]),
    )

    n_block = n_ex = n_q = 0
    for c in chapters:
        seq = c["seq"]
        ch_id = CH_BASE + seq
        cur.execute(
            "INSERT INTO chapter(id,course_id,seq,title_fr,page_start,page_end,"
            "pdf_start,pdf_end,status) VALUES(?,?,?,?,?,?,?,?,'ready')",
            (ch_id, COURSE_ID, seq, c["title_fr"], c["page_start"], c["page_end"],
             c["pdf_start"], c["pdf_end"]),
        )

        # group section_title blocks -> chapter_section rows
        sec_no = 0
        last_sec_id = None
        for b in c["blocks"]:
            if b["type"] == "section_title":
                sec_no += 1
                last_sec_id = SEC_BASE + seq * 100 + sec_no
                cur.execute(
                    "INSERT INTO chapter_section(id,chapter_id,seq,title_fr,page_no) "
                    "VALUES(?,?,?,?,?)",
                    (last_sec_id, ch_id, sec_no, b["body_html"], b["page_no"]),
                )

        for i, b in enumerate(c["blocks"], 1):
            b_id = BLOCK_BASE + seq * 10_000 + i
            cur.execute(
                "INSERT INTO content_block(id,chapter_id,page_no,seq,type,body_html,source_ref) "
                "VALUES(?,?,?,?,?,?,?)",
                (b_id, ch_id, b["page_no"], b["seq"], b["type"], b["body_html"],
                 "p. %d" % b["page_no"]),
            )
            n_block += 1

        for i, e in enumerate(c["exercises"], 1):
            ex_id = EX_BASE + seq * 10_000 + i
            note = e.get("note") or None
            cur.execute(
                "INSERT INTO exercise(id,chapter_id,page_no,seq,instruction_fr,label,"
                "note_html,source_ref,has_image,scan_pages) VALUES(?,?,?,?,?,?,?,?,0,'')",
                (ex_id, ch_id, e.get("page_no") or c["page_start"], e["seq"],
                 e["instruction"], e.get("label") or None,
                 note, "p. %d, ex. %d" % (e.get("page_no") or c["page_start"], i)),
            )
            n_ex += 1
            for j, q in enumerate(e["questions"], 1):
                q_id = Q_BASE + seq * 10_000 + i * 100 + j
                # ensure no collision if an exercise has >99 questions
                while q_id in getattr(main, "_used_q", set()):
                    q_id += 1
                main._used_q = getattr(main, "_used_q", set())
                main._used_q.add(q_id)
                cur.execute(
                    "INSERT INTO question(id,exercise_id,seq,type,stem_html,source_ref,"
                    "answer_json,answer_status,tolerance,grading_mode) "
                    "VALUES(?,?,?,?,?,?,NULL,'pending','normal','auto')",
                    (q_id, ex_id, q["seq"], q["type"], q["stem_html"],
                     "p. %d, ex. %d, q. %s" % (c["page_start"], i, q.get("printed_no", j))),
                )
                n_q += 1

    con.commit()
    print("course id=%d code=%s" % (COURSE_ID, course["code"]))
    print("chapters=%d blocks=%d exercises=%d questions=%d" %
          (len(chapters), n_block, n_ex, n_q))
    for c in chapters:
        print("  %-20s p%d-%d" % (c["code"], c["page_start"], c["page_end"]))
    print("integrity:", cur.execute("PRAGMA quick_check").fetchone()[0])
    con.close()


if __name__ == "__main__":
    main()
