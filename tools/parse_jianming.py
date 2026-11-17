#!/usr/bin/env python3
"""Parse the 简明法语教程 (第3版上册, 孙辉) page transcriptions into course JSON.

Input : data/import/jianming/raw/jNNN.txt   (PDF pages 311-430, see raw/FORMAT_JM.md)
Output: data/import/jianming/jianming.json  {course, chapters:[...]}

Only PDF pages 311 onward (Leçon 25+) are transcribed.  Chapter mapping:

  L25  311-325  p304-318      L28  356-372  p349-365
  L26  326-340  p319-333      L29  373-391  p366-384
  L27  341-355  p334-348      L30  392-410  p385-403
  EXERCICES DE RÉVISION  411-416  p404-409   (separate chapter)
  TABLEAU DE CONJUGAISON 417-430  p410-423   (separate appendix chapter)

Answers are deliberately NOT imported: every question keeps answer_json = None
and answer_status = 'pending'.  Only the stems carry the encoding placeholders:

  {{b}}  a written blank (word / phrase to fill)
  {{L}}  a spoken/dialogue blank (learner's own reply)
  {{T}}  a free-expression blank (Chinese -> French translation, open answer)

Printed underscores come as `___` in the raw text.  A blank at the end of a
Chinese-to-French translation item becomes {{T}}; anything else {{b}}.
"""
import html
import json
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW = os.path.join(ROOT, "data", "import", "jianming", "raw")
OUT = os.path.join(ROOT, "data", "import", "jianming", "jianming.json")

# (seq, code, title_fr, pdf_start, pdf_end, print_start, print_end, kind)
CHAPTERS = [
    (1, "jm_l25", "LEÇON 25 — LES COMPARATIFS", 311, 325, 304, 318, "lesson"),
    (2, "jm_l26", "LEÇON 26 — LES RELATIFS", 326, 340, 319, 333, "lesson"),
    (3, "jm_l27", "LEÇON 27 — PARIS", 341, 355, 334, 348, "lesson"),
    (4, "jm_l28", "LEÇON 28 — LE FUTUR SIMPLE", 356, 372, 349, 365, "lesson"),
    (5, "jm_l29", "LEÇON 29 — LE DOCTEUR", 373, 391, 366, 384, "lesson"),
    (6, "jm_l30", "LEÇON 30 — L'AMÉRICANISATION", 392, 410, 385, 403, "lesson"),
    (7, "jm_revision", "EXERCICES DE RÉVISION", 411, 416, 404, 409, "revision"),
    (8, "jm_conjugaison", "TABLEAU DE CONJUGAISON", 417, 430, 410, 423, "appendix"),
]

SEC_RE = re.compile(r"^@SEC\s+(.+)$")
PAGE_RE = re.compile(r"^@PAGE\s+(\d+)$")
P_RE = re.compile(r"^@P\s?(.*)$")
EX_RE = re.compile(r"^@EX\s+([^|]+)\|(.*)$")
Q_RE = re.compile(r"^@Q\s+([^|]+)\|(.*)$")
NOTE_RE = re.compile(r"^@NOTE\s+(.*)$")

# 以中文为主的题干 => 视作开放翻译题；整句末尾的空用 {{T}}
CJK = re.compile(r"[\u4e00-\u9fff]")


def esc(s):
    return html.escape(s, quote=False)


def inline(s):
    """Light inline markup: superscript notes stay literal, underscores -> blank."""
    s = esc(s)
    s = re.sub(r"\^(\d+)", r"<sup>\1</sup>", s)
    return s


def to_blanks(text):
    """Convert ___ runs into placeholders. Detect translation items."""
    n = text.count("___")
    if not n:
        return text, 0
    is_translation = bool(CJK.search(text))
    ph = "{{T}}" if is_translation else "{{b}}"
    return text.replace("___", ph), n


def split2(line):
    """Return (left, right) on a ` || ` separator, else (None, line)."""
    if " || " in line:
        a, b = line.split(" || ", 1)
        return a.strip(), b.strip()
    return None, line.strip()


def is_table(line):
    return " | " in line and " || " not in line


def render_table(rows):
    """rows: list of cell-lists. Emit a simple HTML table."""
    if not rows:
        return ""
    ncol = max(len(r) for r in rows)
    out = ["<table class='jm-table'>"]
    for i, r in enumerate(rows):
        tag = "th" if i == 0 else "td"
        cells = "".join("<%s>%s</%s>" % (tag, inline(c), tag) for c in r)
        out.append("<tr>%s</tr>" % cells)
    out.append("</table>")
    return "".join(out)


def render_two_col(pairs):
    """pairs: list of (left, right)."""
    out = ["<div class='two-col'>"]
    for a, b in pairs:
        out.append("<div>%s</div><div>%s</div>" % (inline(a), inline(b)))
    out.append("</div>")
    return "".join(out)


def read_pages():
    pages = {}
    for fn in os.listdir(RAW):
        m = re.fullmatch(r"j(\d+)\.txt", fn)
        if not m:
            continue
        pno = int(m.group(1))
        if pno < 311 or pno > 430:
            continue
        lines = []
        with open(os.path.join(RAW, fn), encoding="utf-8") as f:
            for raw in f:
                line = raw.rstrip("\n").rstrip("\r")
                if not line.strip():
                    continue
                lines.append(line)
        pages[pno] = lines
    return pages


def parse_lines(lines, pdf_page, print_page):
    """Return (blocks, exercises) for one page.

    blocks: [{type, body_html}]; exercises: [{label, instruction, note, questions:[]}]
    Cross-page continuations are stitched by the caller.
    """
    blocks, exercises = [], []
    section = None
    cur_ex = None
    paras = []       # consecutive @P lines to merge into one paragraph
    two_col = []     # consecutive two-column lexicon rows
    table_rows = []  # consecutive table rows

    def kind_for(line):
        """Decide body_html type from current section + line shape."""
        s = section or ""
        if is_table(line):
            return "table"
        if " || " in line:
            return "twocol"
        if "Points importants" in s:
            return "keypoint"
        if s in ("Façons de parler", "Le français de communication",
                 "Le français de communication 交际法语", "Faire des achats 购物",
                 "Souhait et compliment 祝愿与恭贺"):
            return "speech"
        if s in ("LEXIQUE", "Lexique"):
            return "twocol"
        if s in ("NOTES", "GRAMMAIRE", "COMPLÉMENT", "MOTS ET EXPRESSIONS"):
            return "para"
        return "para"

    def flush():
        nonlocal paras, two_col, table_rows
        if table_rows:
            blocks.append({"type": "table", "body_html": render_table(table_rows)})
            table_rows = []
        if two_col:
            blocks.append({"type": "table", "body_html": render_two_col(two_col)})
            two_col = []
        if paras:
            body = "<br>".join(inline(x) for x in paras)
            blocks.append({"type": "para", "body_html": body})
            paras = []

    for line in lines:
        if line.strip() == "@EMPTY":
            flush()
            continue
        m = PAGE_RE.match(line)
        if m:
            continue
        m = SEC_RE.match(line)
        if m:
            flush()
            section = m.group(1).strip()
            # section heading becomes a visible block except exercise-only heads
            if section not in ("EXERCICES", "EXERCICES DE RÉVISION"):
                blocks.append({"type": "section_title", "body_html": inline(section)})
            continue
        m = EX_RE.match(line)
        if m:
            flush()
            cur_ex = {"label": m.group(1).strip(), "instruction": m.group(2).strip(),
                      "note": "", "questions": [], "page_no": print_page}
            exercises.append(cur_ex)
            continue
        m = Q_RE.match(line)
        if m:
            flush()
            if cur_ex is None:
                cur_ex = {"label": "", "instruction": "", "note": "", "questions": [],
                          "page_no": print_page}
                exercises.append(cur_ex)
            raw = m.group(2).strip()
            stem, nblank = to_blanks(raw)
            qtype = "open" if "{{T}}" in stem else ("cloze" if nblank else "open")
            cur_ex["questions"].append({
                "printed_no": m.group(1).strip(),
                "type": qtype,
                "stem_html": inline(stem),
                "nblank": nblank,
            })
            continue
        m = NOTE_RE.match(line)
        if m:
            flush()
            blocks.append({"type": "remark", "body_html": inline(m.group(1))})
            continue
        m = P_RE.match(line)
        if m:
            text = m.group(1)
            # inside an exercise, loose @P lines are either the example/model or
            # continuation of the last question's stem
            if cur_ex is not None and section in ("EXERCICES", "EXERCICES DE RÉVISION"):
                if cur_ex["questions"]:
                    q = cur_ex["questions"][-1]
                    more, nb = to_blanks(text)
                    q["stem_html"] += "<br>" + inline(more)
                    q["nblank"] += nb
                    if nb and q["type"] == "open" and "{{T}}" not in more:
                        q["type"] = "cloze"
                else:
                    cur_ex["note"] += ("<br>" if cur_ex["note"] else "") + inline(text)
                continue
            k = kind_for(text)
            if k == "table":
                flush()
                table_rows.append([c.strip() for c in text.split(" | ")])
            elif k == "twocol":
                flush()
                a, b = split2(text)
                two_col.append((a or "", b or ""))
            else:
                # a two-col lexicon section: keep flushing paragraph boundaries
                if two_col:
                    flush()
                paras.append(text)
            continue
    flush()
    return blocks, exercises


def merge_exercises(exercises, incoming):
    """Stitch same-label exercises across pages; append otherwise."""
    for ex in incoming:
        label = ex["label"]
        # A page that begins with bare @Q (no @EX) is a continuation of the
        # previous exercise.  It is parsed into a placeholder exercise with
        # empty label/instruction -- fold it back into the real one.
        if not label and not ex["instruction"] and exercises:
            exercises[-1]["questions"].extend(ex["questions"])
            if ex["note"]:
                exercises[-1]["note"] += (" " if exercises[-1]["note"] else "") + ex["note"]
            continue
        if ex["instruction"] and exercises and exercises[-1]["label"] == label:
            # explicitly repeated @EX with the same label on the next page
            exercises[-1]["questions"].extend(ex["questions"])
            if ex["note"]:
                exercises[-1]["note"] += (" " if exercises[-1]["note"] else "") + ex["note"]
            continue
        exercises.append(ex)


def main():
    pages = read_pages()
    out_chapters = []
    for seq, code, title, p0, p1, pr0, pr1, kind in CHAPTERS:
        blocks, exercises = [], []
        for pno in range(p0, p1 + 1):
            if pno not in pages:
                print("MISSING page", pno)
                continue
            b, e = parse_lines(pages[pno], pno, pno - 7)
            for blk in b:
                blk["page_no"] = pno - 7
                blocks.append(blk)
            merge_exercises(exercises, e)

        # assign stable seq within chapter
        for i, blk in enumerate(blocks, 1):
            blk["seq"] = i
        for i, ex in enumerate(exercises, 1):
            ex["seq"] = i
            for j, q in enumerate(ex["questions"], 1):
                q["seq"] = j

        out_chapters.append({
            "seq": seq, "code": code, "title_fr": title, "kind": kind,
            "page_start": pr0, "page_end": pr1, "pdf_start": p0, "pdf_end": p1,
            "blocks": blocks, "exercises": exercises,
        })

    data = {
        "course": {
            "code": "jianming_3e_upper",
            "title_fr": "简明法语教程（第3版）上册",
            "level": "",
            "source_book": "孙辉 — 简明法语教程（第3版）上册，商务印书馆",
        },
        "chapters": out_chapters,
    }
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=1)

    nb = sum(len(c["blocks"]) for c in out_chapters)
    ne = sum(len(c["exercises"]) for c in out_chapters)
    nq = sum(len(e["questions"]) for c in out_chapters for e in c["exercises"])
    print("wrote", OUT)
    print("chapters=%d blocks=%d exercises=%d questions=%d" % (len(out_chapters), nb, ne, nq))
    for c in out_chapters:
        nq2 = sum(len(e["questions"]) for e in c["exercises"])
        print("  %-26s p%d-%d blocks=%3d ex=%3d q=%3d" %
              (c["code"], c["page_start"], c["page_end"], len(c["blocks"]),
               len(c["exercises"]), nq2))


if __name__ == "__main__":
    main()
