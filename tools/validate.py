#!/usr/bin/env python3
"""Validate data/chapters/*.json against the verbatim-transcription schema.

Run after each batch:  python tools/validate.py [--check-scans]
"""
import json
import os
import re
import sys
from collections import Counter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
META = json.load(open(os.path.join(ROOT, "tools", "chapter_pages.json"), encoding="utf-8"))
CHAPTER_DIR = os.path.join(ROOT, "data", "chapters")
PH = re.compile(r"\{\{([bLTco])(!?)(?::([^}]*))?\}\}")
COURSE = {"g": "grammaire", "v": "vocabulaire"}
FNAME = re.compile(r"^([gv])(\d{2})\.json$")


def check(data, meta_c, errors, warnings, check_scans):
    book = data["course"]
    seq = data["seq"]
    tag = "%s-%02d" % (book[0], seq)
    m = next((c for c in meta_c["chapters"] if c["seq"] == seq), None)
    if m is None:
        errors.append("%s: seq %d not in TOC" % (tag, seq))
        return
    for k in ("page_start", "page_end", "pdf_start", "pdf_end", "title_fr"):
        if data.get(k) != m[k]:
            errors.append("%s: %s = %r, expected %r" % (tag, k, data.get(k), m[k]))
    allpages = set(m["lesson_pages"]) | set(m["exercise_pages"])
    lesson = set(m["lesson_pages"])
    exercise = set(m["exercise_pages"])

    for key in ("sections", "blocks", "exercises"):
        if not isinstance(data.get(key), list):
            errors.append("%s: missing list %s" % (tag, key))
            return
    for i, s in enumerate(data["sections"], 1):
        if not s.get("title_fr") or s.get("page_no") not in allpages:
            errors.append("%s: section %d bad page/title" % (tag, i))

    bseq = [b["seq"] for b in data["blocks"]]
    if bseq != list(range(1, len(bseq) + 1)):
        errors.append("%s: block seq not contiguous 1..n" % tag)
    for b in data["blocks"]:
        if b["page_no"] not in lesson:
            warnings.append("%s: block %d on page %d (not a lesson page)" % (tag, b["seq"], b["page_no"]))
        if not b.get("body_html") or not b.get("type"):
            errors.append("%s: block %d empty body/type" % (tag, b["seq"]))
        if "______" in b["body_html"]:
            errors.append("%s: block %d has raw blank lines" % (tag, b["seq"]))

    eseq = [e["seq"] for e in data["exercises"]]
    if eseq != list(range(1, len(eseq) + 1)):
        errors.append("%s: exercise seq not contiguous 1..n" % tag)
    nq = 0
    nw = 0
    nimg = 0
    for e in data["exercises"]:
        if e["page_no"] not in exercise:
            errors.append("%s: exercise %d on page %d (not an exercise page)" % (tag, e["seq"], e["page_no"]))
        if not e.get("instruction_fr"):
            errors.append("%s: exercise %d empty instruction" % (tag, e["seq"]))
        qseq = [q["seq"] for q in e["questions"]]
        if qseq != list(range(1, len(qseq) + 1)):
            errors.append("%s: exercise %d question seq not contiguous" % (tag, e["seq"]))
        if not e["questions"]:
            errors.append("%s: exercise %d has no questions" % (tag, e["seq"]))
        for q in e["questions"]:
            nq += 1
            stem = q.get("stem_html", "")
            if "______" in stem or "___" in stem:
                errors.append("%s: exercise %d q%d has raw blank lines" % (tag, e["seq"], q["seq"]))
            stripped = PH.sub("", stem)
            if "{{" in stripped or "}}" in stripped:
                errors.append("%s: exercise %d q%d has malformed placeholder" % (tag, e["seq"], q["seq"]))
            nw += len(PH.findall(stem))
        if e.get("has_image"):
            nimg += 1
            pages = e.get("scan_pages") or []
            if not pages:
                errors.append("%s: exercise %d has_image but no scan_pages" % (tag, e["seq"]))
            for p in pages:
                if p not in allpages:
                    errors.append("%s: exercise %d scan page %d outside chapter" % (tag, e["seq"], p))
                if check_scans:
                    f = os.path.join(ROOT, meta_c["scan_dir"], "p%d.jpg" % p)
                    if not os.path.isfile(f):
                        warnings.append("%s: scan missing %s" % (tag, f))
    return {"blocks": len(data["blocks"]), "exercises": len(data["exercises"]),
            "questions": nq, "widgets": nw, "image_exercises": nimg}


def main():
    check_scans = "--check-scans" in sys.argv
    errors, warnings = [], []
    stats = Counter()
    seen = {k: set() for k in META}
    files = sorted(f for f in os.listdir(CHAPTER_DIR) if FNAME.match(f)) if os.path.isdir(CHAPTER_DIR) else []
    for fn in files:
        mo = FNAME.match(fn)
        book = COURSE[mo.group(1)]
        seq = int(mo.group(2))
        try:
            data = json.load(open(os.path.join(CHAPTER_DIR, fn), encoding="utf-8"))
        except Exception as e:
            errors.append("%s: invalid JSON: %s" % (fn, e))
            continue
        if data.get("course") != book or data.get("seq") != seq:
            errors.append("%s: course/seq mismatch with filename" % fn)
        seen[book].add(seq)
        s = check(data, META[book], errors, warnings, check_scans)
        if s:
            for k, v in s.items():
                stats[book + "." + k] += v
    for book, cfg in META.items():
        expected = set(c["seq"] for c in cfg["chapters"] if c["seq"] >= 2)
        missing = sorted(expected - seen[book])
        print("%-12s done %2d/%-2d  missing: %s" %
              (book, len(seen[book] & expected), len(expected),
               ",".join(str(x) for x in missing) if missing else "none"))
        print("             blocks=%d exercises=%d questions=%d widgets=%d image_ex=%d" %
              (stats[book + ".blocks"], stats[book + ".exercises"], stats[book + ".questions"],
               stats[book + ".widgets"], stats[book + ".image_exercises"]))
    print("\nerrors=%d warnings=%d files=%d" % (len(errors), len(warnings), len(files)))
    for e in errors[:60]:
        print("  ERROR " + e)
    if len(errors) > 60:
        print("  ... %d more errors" % (len(errors) - 60))
    for w in warnings[:12]:
        print("  WARN  " + w)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
