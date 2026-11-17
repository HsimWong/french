#!/usr/bin/env python3
"""Parse data/import/answers/raw/pNNN.txt (verbatim Corrigés transcriptions) into
data/import/answers/gNN.json (importer input) and report every mismatch.

Mapping: @CH n / @PAGE p / @EX k / "<printed>. <text>"  ->
  chapter seq n, exercise page_no p, exercise order k on that page,
  question = the one whose printed number in the stem matches, falling back to
  the question seq of the same value.

That distinction matters: the seeded stems keep the book's own numbering
("2. Vous allez au restaurant…") while the DB `seq` counts positions, so for 26
exercises the two disagree by one. Matching on the printed number keeps answers
on the right question.

Blank extraction
---------------
The corrigé brackets blanks with an ellipsis (…) but not uniformly:
  "… sont réservés."                -> answer after the mark
  "… la … la … la …"                -> three answers, plus a dangling mark
  "une …"                            -> answer before the mark
  "… intelligent, créatif, original…"-> trailing mark is a list ellipsis
So: split on "…" and keep the NON-EMPTY segments, accepted only when the count
equals the number of placeholders in the stem.

Unnumbered blocks ("- " lines) are also tried, with several split hypotheses,
keeping whichever total equals the number of missing blanks.

Overrides in data/import/answers/over/gNN.json (same shape, keyed by DB seq) are
applied last and validated the same way.
"""
import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW = os.path.join(ROOT, "data", "import", "answers", "raw")
OUT = os.path.join(ROOT, "data", "import", "answers")
OVER = os.path.join(OUT, "over")
SPEC = os.path.join(OUT, "_spec.json")

SKIP_FILES = {"p051"}  # duplicate scan of the same printed page

CH_RE = re.compile(r"^@CH\s+(-?\d+)")
PAGE_RE = re.compile(r"^@PAGE\s+(\d+)")
EX_RE = re.compile(r"^@EX\s+(\d+)")
ITEM_RE = re.compile(r"^(\d{1,2})\.\s*(.*)$")
SPLIT_RE = re.compile(r"\s(?=\d{1,2}\.\s)")
LEADNUM = re.compile(r"^\s*(\d{1,2})\.")
LABELS = ("Réponses possibles", "Réponse possible", "Exemples", "Exemple",
          "Suite possible", "Questions possibles", "Autres possibilités",
          "Plusieurs réponses", "Suggestions", "Corrigé", "Autres exemples")


def clean(s):
    s = s.replace("\u00a0", " ").strip()
    s = re.sub(r"\s+", " ", s)
    s = s.replace("\u2026", " ").strip()
    s = re.sub(r"\s+", " ", s)
    return s.strip(" .;,:«»")


def as_variant(s):
    """'le/la' -> 'le|la' so the grader accepts either spelling.
    Guarded: never split when a digit sits next to the slash (dates, ratios)."""
    if "/" not in s:
        return s
    parts = [p.strip() for p in s.split("/")]
    if len(parts) < 2 or not all(parts):
        return s
    for p in parts:
        if re.search(r"\d\s*$", p) or re.match(r"\s*\d", p):
            return s
    return "|".join(parts)


def split_blanks(text):
    if "\u2026" not in text:
        t = clean(text)
        return [as_variant(t)] if t else []
    return [as_variant(b) for b in (clean(p) for p in text.split("\u2026")) if b]


def load_raw():
    cells = {}
    conflicts = []
    for path in sorted(glob.glob(os.path.join(RAW, "p*.txt"))):
        base = os.path.basename(path)[:-4]
        if base in SKIP_FILES:
            continue
        ch = page = ex = None
        for line in open(path, encoding="utf-8"):
            line = line.rstrip("\n").strip()
            if not line or line.startswith("#"):
                continue
            m = CH_RE.match(line)
            if m:
                ch, page, ex = int(m.group(1)), None, None
                continue
            m = PAGE_RE.match(line)
            if m:
                page, ex = int(m.group(1)), None
                continue
            m = EX_RE.match(line)
            if m:
                ex = int(m.group(1))
                continue
            if line == "@EMPTY" or ch is None or page is None or ex is None:
                continue
            cell = cells.setdefault((ch, page, ex), {"items": {}, "blocks": []})
            if line.startswith("-"):
                cell["blocks"].append(line[1:].strip())
                continue
            for piece in SPLIT_RE.split(line):
                m = ITEM_RE.match(piece.strip())
                if not m:
                    continue
                seq = int(m.group(1))
                text = m.group(2).strip()
                d = cell["items"]
                if seq in d and d[seq] != text:
                    conflicts.append((base, ch, page, ex, seq, d[seq], text))
                    continue
                d[seq] = text
    return cells, conflicts


def main():
    spec = json.load(open(SPEC, encoding="utf-8"))
    cells, conflicts = load_raw()

    # chapter -> page -> [ {seq,key,nb} ... ] in print order
    chapters = {}
    for chs, chdata in spec.items():
        ch = int(chs)
        pages = {}
        for pg, exlist in chdata["pages"].items():
            exs = []
            for ex in exlist:
                qs = []
                for q in ex["q"]:
                    m = LEADNUM.match(q["stem"] or "")
                    qs.append({"seq": q["seq"], "nb": q["nb"],
                               "key": int(m.group(1)) if m else q["seq"]})
                exs.append(qs)
            pages[int(pg)] = exs
        chapters[ch] = pages

    per_chapter = {}
    stats = dict.fromkeys(("mapped", "merged", "blocks_fill", "blocks_fail",
                           "nb_mismatch", "no_question", "ex_out_of_range",
                           "zero_blank", "no_data", "override", "over_bad"), 0)
    problems = []

    def put(ch, page, ex, seq, blanks):
        d = per_chapter.setdefault(ch, {}).setdefault(str(page), {}).setdefault(str(ex), {})
        d[str(seq)] = blanks[0] if len(blanks) == 1 else blanks
        stats["mapped"] += 1

    def fill(ch, page, ex, q, text, where):
        nb = q["nb"]
        if nb == 0:
            stats["zero_blank"] += 1
            return
        blanks = split_blanks(text)
        if nb == 1 and len(blanks) == 2 and all(blanks):
            blanks = ["|".join(blanks)]
        if len(blanks) != nb:
            problems.append((where, ch, page, ex,
                             "q%d: %d vs %d | %s" % (q["seq"], len(blanks), nb, text[:80])))
            stats["nb_mismatch"] += 1
            return
        put(ch, page, ex, q["seq"], blanks)

    for (ch, page, ex), cell in sorted(cells.items()):
        if ch not in chapters or page not in chapters[ch]:
            if ch in chapters:
                problems.append(("PAGE?", ch, page, ex,
                                 "chapter pages %s" % sorted(chapters[ch])))
                stats["no_data"] += 1
            continue
        exlist = chapters[ch][page]
        if ex > len(exlist):
            problems.append(("EX?", ch, page, ex, "page has %d exercises" % len(exlist)))
            stats["ex_out_of_range"] += 1
            continue
        qs = exlist[ex - 1]

        # a whole table stored as ONE question with N blanks, keyed 1..N
        if len(qs) == 1 and len(cell["items"]) > 1:
            flat = []
            for seq in sorted(cell["items"]):
                flat.extend(split_blanks(cell["items"][seq]))
            if len(flat) == qs[0]["nb"]:
                put(ch, page, ex, qs[0]["seq"], flat)
                stats["merged"] += 1
                continue

        done_seq = set()
        for printed, text in sorted(cell["items"].items()):
            target = next((q for q in qs if q["key"] == printed), None)
            if target is None:
                target = next((q for q in qs if q["seq"] == printed), None)
            if target is None:
                problems.append(("Q?", ch, page, ex,
                                 "no question for printed %d (segs %s / keys %s)"
                                 % (printed, [q["seq"] for q in qs], [q["key"] for q in qs])))
                stats["no_question"] += 1
                continue
            before = stats["nb_mismatch"]
            fill(ch, page, ex, target, text, "NB")
            if stats["nb_mismatch"] == before:
                done_seq.add(target["seq"])

        # unnumbered list blocks: try split hypotheses, keep the one that fits
        if cell["blocks"]:
            missing = [q for q in qs if q["seq"] not in done_seq and q["nb"] > 0]
            need = sum(q["nb"] for q in missing)
            usable = [b for b in cell["blocks"]
                      if not any(b.startswith(l) for l in LABELS)]
            if need and usable:
                joined = " ".join(usable)
                pools = []
                if any("\u2026" in b for b in usable):
                    pools.append(("ellipsis", [v for b in usable for v in split_blanks(b)]))
                for sep, name in ((",", "comma"), (" \u2013 ", "dash"),
                                  (" / ", "slash"), (";", "semicolon")):
                    pools.append((name, [p for p in (clean(x) for x in joined.split(sep)) if p]))
                paren = [re.sub(r"\s*\([^)]*\)", "", p)
                         for p in (clean(x) for x in joined.replace(" \u2013 ", ",").split(",")) if p]
                pools.append(("noparen", [p for p in paren if p]))
                hit = next((p for p in pools if len(p[1]) == need), None)
                if hit:
                    it = iter(hit[1])
                    for q in missing:
                        put(ch, page, ex, q["seq"],
                            [next(it) for _ in range(q["nb"])])
                    stats["blocks_fill"] += 1
                    problems.append(("BLOCKS", ch, page, ex,
                                     "filled %d blanks / %d q via %s" % (need, len(missing), hit[0])))
                else:
                    problems.append(("BLOCKS?", ch, page, ex,
                                     "need %d, candidates %s | %s"
                                     % (need, {n: len(v) for n, v in pools}, joined[:100])))
                    stats["blocks_fail"] += 1

    # overrides: keyed by DB seq, same acceptance test
    for path in sorted(glob.glob(os.path.join(OVER, "g*.json"))):
        ch = int(os.path.basename(path)[1:3])
        data = json.load(open(path, encoding="utf-8"))
        for pg, pm in data.get("pages", {}).items():
            page = int(pg)
            if ch not in chapters or page not in chapters[ch]:
                continue
            for exs, qm in pm.items():
                ex = int(exs)
                if ex < 1 or ex > len(chapters[ch][page]):
                    continue
                for seqs, val in qm.items():
                    seq = int(seqs)
                    q = next((x for x in chapters[ch][page][ex - 1] if x["seq"] == seq), None)
                    if not q:
                        continue
                    blanks = val if isinstance(val, list) else [val]
                    blanks = [as_variant(clean(str(b))) for b in blanks]
                    if len(blanks) != q["nb"] or not all(blanks):
                        stats["over_bad"] += 1
                        problems.append(("OVER", ch, page, ex,
                                         "q%d: %d vs %d rejected" % (seq, len(blanks), q["nb"])))
                        continue
                    put(ch, page, ex, seq, blanks)
                    stats["override"] += 1

    written = 0
    for ch, pages in sorted(per_chapter.items()):
        with open(os.path.join(OUT, "g%02d.json" % ch), "w", encoding="utf-8") as f:
            json.dump({"pages": pages}, f, ensure_ascii=False, indent=1)
            f.write("\n")
        written += sum(len(q) for p in pages.values() for q in p.values())

    for k in ("mapped", "override", "over_bad", "merged", "blocks_fill",
              "blocks_fail", "nb_mismatch", "no_question", "ex_out_of_range",
              "zero_blank", "no_data"):
        print("%-15s %d" % (k, stats[k]))
    print("answers written: %d" % written)
    print("conflicts: %d" % len(conflicts))

    with open(os.path.join(OUT, "_report.txt"), "w", encoding="utf-8") as f:
        for p in problems:
            f.write("%s\n" % (p,))
    bykind = {}
    for p in problems:
        bykind[p[0]] = bykind.get(p[0], 0) + 1
    print("problems by kind:", bykind)
    return 0


if __name__ == "__main__":
    sys.exit(main())
