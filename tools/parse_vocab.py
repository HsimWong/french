#!/usr/bin/env python3
"""Parse the Vocabulaire Progressif (A2/B1) Corrigés transcription.

Unlike the grammar answer key, the vocabulary key cannot be copied verbatim:
the exercise page submits *encoded* widget values.

  {{b}} {{L}} {{T}}  -> the text typed by the learner
  {{o:A|B|C}}        -> the 1-based option INDEX ("2", "2,3" for multi-select)
  {{c:g}} {{c:g}}    -> a mutually exclusive tick; the chosen one is "1"

So every printed answer is translated according to the question's placeholder
list before it is stored.  Output answer objects are *sparse*: only blanks that
carry a meaningful value get a key (an unticked checkbox is omitted, otherwise
matchAnswer() would count it as an unreachable blank).

Alignment is NOT positional.  Some book pages were split into more exercises in
the database than the key has circled numbers (e.g. ch3 p.19: 6 exercises vs 3
circled answers, and the 3 answers belong to exercises 4-6).  For each page we
score every (answer cell, exercise) pair by how many questions can actually be
encoded, then take the best order-preserving matching with a DP.

Input : data/import/answers_vocab/raw/v*.txt   (see raw/FORMAT_VOCAB.md)
Output: data/import/answers_vocab/vNN.json     {pages:{page:{ex:{qseq:{"1":[variants]}}}}}
        data/import/answers_vocab/_report.txt
"""
import collections
import glob
import json
import os
import re
import sqlite3
import unicodedata

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW = os.path.join(ROOT, "data", "import", "answers_vocab", "raw")
OUTDIR = os.path.join(ROOT, "data", "import", "answers_vocab")
DB = os.path.join(ROOT, "french.db")

PH = re.compile(r"\{\{([bLTco])(!?)(?::([^}]*))?\}\}")
ITEM = re.compile(r"(?<![\w])(\d{1,2})[.:]\s+(?=\S)")
CH = re.compile(r"^@CH\s+(\d+)")
PG = re.compile(r"^@PAGE\s+(\d+)")
EX = re.compile(r"^@EX\s+(\d+)")


def clean(s):
    """Undo print artefacts; keep the answer wording itself."""
    s = s.replace("\u2019", "'").replace("\u02bc", "'").replace("`", "'")
    # words broken across printed lines: "jour- nal" -> "journal"
    s = re.sub(r"([A-Za-z\u00c0-\u017f])-\s+([A-Za-z\u00c0-\u017f])", r"\1\2", s)
    s = re.sub(r"\s+", " ", s)
    s = s.replace("\u2026", " ").replace("...", " ")
    s = re.sub(r"\s+", " ", s)
    return s.strip(" .;,:!?\u2013\u2014-")


def variants(s):
    """'A / B' (slashes with spaces) = alternative acceptable answers."""
    return [v for v in (clean(p) for p in re.split(r"\s*/\s*", s)) if v]


def norm(s):
    s = clean(s).lower()
    return "".join(c for c in s if not unicodedata.combining(c))


def fold(s):
    return "".join(c for c in unicodedata.normalize("NFD", s)
                   if not unicodedata.combining(c))


def option_index(cand, opts, tag=""):
    """Map an answer wording (or a/b/c letter) to a 1-based option index."""
    c = norm(cand)
    if not c:
        return None
    for i, o in enumerate(opts):
        if c == norm(o):
            return i + 1
    # The key frequently labels the options a/b/c rather than quoting them.
    if re.fullmatch(r"[a-h]", c) and len(opts) <= 8:
        return ord(c) - ord("a") + 1
    for i, o in enumerate(opts):
        if fold(c) == fold(norm(o)):
            return i + 1
    # The key sometimes adds/drops an article or a short qualifier.
    for i, o in enumerate(opts):
        a, b = fold(norm(o)), fold(c)
        if a and b and min(len(a), len(b)) >= 3 and (a in b or b in a):
            return i + 1
    # A broken-word transcription ("déco- chons" for "décrochons") can differ
    # by one letter; accept a single, clearly best fuzzy match only.
    import difflib
    scored = sorted(((difflib.SequenceMatcher(None, fold(c), fold(norm(o))).ratio(), i)
                     for i, o in enumerate(opts)), reverse=True)
    if scored and scored[0][0] >= 0.86 and (len(scored) == 1
                                            or scored[0][0] - scored[1][0] >= 0.12):
        return scored[0][1] + 1
    return None


def split_items(text):
    """Split '- 1. A – 2. B – 3. C.' into {1:'A', 2:'B', 3:'C'}."""
    cand, last = [], 0
    for m in ITEM.finditer(text):
        n = int(m.group(1))
        if 1 <= n <= 40 and n > last:
            cand.append((n, m))
            last = n
    if not cand:
        return {}, text.strip()
    out = {}
    for i, (n, m) in enumerate(cand):
        end = cand[i + 1][1].start() if i + 1 < len(cand) else len(text)
        seg = text[m.end():end].strip()
        seg = re.sub(r"\s*[\u2013\u2014]\s*$", "", seg).strip()
        out[n] = seg
    return out, text[:cand[0][1].start()].strip()


def split_flat(text):
    """Unnumbered answer block -> pieces split on en-dash / comma."""
    t = re.sub(r"\s*[\u2013\u2014]\s*", ",", text)
    return [p for p in (clean(x) for x in t.split(",")) if p]


def load_cells():
    cells = collections.OrderedDict()
    ch = pg = ex = None
    for path in sorted(glob.glob(os.path.join(RAW, "v*.txt"))):
        for line in open(path, encoding="utf-8"):
            line = line.strip()
            m = CH.match(line)
            if m:
                ch = int(m.group(1)); pg = ex = None
                continue
            m = PG.match(line)
            if m:
                pg = int(m.group(1)); ex = None
                continue
            m = EX.match(line)
            if m:
                ex = int(m.group(1))
                continue
            if line.startswith("- ") and ch and pg and ex:
                cells.setdefault((ch, pg, ex), []).append(line[2:].strip())
    return {k: " | ".join(v) for k, v in cells.items()}


def load_spec():
    con = sqlite3.connect(DB)
    con.row_factory = sqlite3.Row
    spec = collections.defaultdict(dict)
    for r in con.execute("""SELECT ch.seq sq, e.page_no pg, e.id eid, e.seq es,
             q.seq qs, q.type tp, q.stem_html s
        FROM question q JOIN exercise e ON e.id=q.exercise_id
        JOIN chapter ch ON ch.id=e.chapter_id
        WHERE ch.course_id=2 ORDER BY ch.seq, e.page_no, e.seq, q.seq"""):
        spec[(r["sq"], r["pg"])].setdefault(r["eid"], []).append({
            "seq": r["qs"], "type": r["tp"], "ph": PH.findall(r["s"] or ""),
            "stem": r["s"] or ""})
    con.close()
    return spec


def printed_number(q):
    m = re.match(r"^\s*(\d{1,2})\.", re.sub(r"<[^>]+>", "", q["stem"]))
    return int(m.group(1)) if m else q["seq"]


def tick_labels(stem):
    """Words printed immediately before each {{c:g}} tick box."""
    plain = re.sub(r"<[^>]+>", " ", stem)
    parts = re.split(r"\{\{c:[^}]*\}\}", plain)
    out = []
    for p in parts[:-1]:
        m = re.search(r"([A-Za-z\u00c0-\u017f'\u2019\u0153-]+)\s*$", p.strip())
        out.append(m.group(1).rstrip(".") if m else "")
    return out


def text_blanks(seg, nblank):
    """One printed answer -> nblank blank values (each a list of variants)."""
    if nblank == 1:
        v = variants(seg)
        return [v] if v else None
    for pattern in (r"\s*,\s*", r"\s*/\s*", r"\s+et\s+",
                    r"\s*[\u2013\u2014-]\s*"):
        parts = [p for p in (clean(x) for x in re.split(pattern, seg)) if p]
        if len(parts) == nblank:
            return [variants(p) or [p] for p in parts]
    return None


def encode(q, seg, problems, tag):
    """Return the sparse answer object for one question, or None."""
    ph = q["ph"]
    kinds = [p[0] for p in ph]
    nb = len(ph)
    if nb == 0:
        return None
    if "T" in kinds:                       # free composition: no objective key
        problems.append((tag, "FREE-T"))
        return None
    if "[FIGURE]" in seg or "[?]" in seg:
        problems.append((tag, "FIGURE"))
        return None
    seg = clean(seg)
    if not seg:
        problems.append((tag, "EMPTY"))
        return None

    # ---- tick groups: Vrai/Faux, Amour/Amitié, a./b./c. --------------------
    if "c" in kinds:
        cidx = [i + 1 for i, k in enumerate(kinds) if k == "c"]
        bidx = [i + 1 for i, k in enumerate(kinds) if k in "bL"]
        labels = tick_labels(q["stem"])
        head = re.match(r"^([A-Za-z\u00c0-\u017f]+)\b", seg)
        head_txt = head.group(1) if head else ""
        picked = []
        for cand in re.split(r"\s*/\s*|\s*,\s*", head_txt):
            c = norm(cand)
            hit = None
            for pos, lab in enumerate(labels[:len(cidx)]):
                if c and (c == norm(lab) or fold(c) == fold(norm(lab))):
                    hit = cidx[pos]; break
            if hit is None:
                if c in ("v", "vrai") and cidx:
                    hit = cidx[0]
                elif c in ("f", "faux") and len(cidx) > 1:
                    hit = cidx[1]
                elif re.fullmatch(r"[a-h]", c or "") and len(cidx) <= 8:
                    j = ord(c) - ord("a")
                    if j < len(cidx):
                        hit = cidx[j]
            if hit is not None and hit not in picked:
                picked.append(hit)
        if not picked:
            problems.append((tag, "TICK? %s | labels=%s" % (seg[:55], labels[:4])))
            return None
        # Ticks in one question share a group (mutually exclusive): accepting
        # several ticks would add unreachable blanks, so keep the first one.
        out = {str(picked[0]): ["1"]}
        rest = seg[len(head_txt):].lstrip(" \u2192\u2013\u2014-:.,")
        rest = clean(rest)
        if rest and bidx:
            vals = text_blanks(rest, len(bidx))
            if vals:
                for k, v in zip(bidx, vals):
                    out[str(k)] = v
        return out

    # ---- clickable options (the widget submits option INDEXES) -------------
    if "o" in kinds and nb == 1:
        arg, bang = ph[0][2] or "", ph[0][1]
        opts = [clean(o) for o in arg.split("|")]
        cands = [c for c in (clean(p) for p in re.split(r"\s*/\s*|\s*,\s*", seg)) if c]
        idxs = []
        for c in cands:
            j = option_index(c, opts, tag)
            if j is None or j > len(opts):
                problems.append((tag, "OPT? %s || %s" % (c[:45], arg[:70])))
                return None
            idxs.append(str(j))
        if not idxs:
            return None
        return {"1": idxs}

    # ---- several blanks in one stem, at least one of them an option group ---
    # e.g. "Je {{o:m'habiller|retirer|me changer}}, je {{o:mets|suis|reste}}..."
    # The key gives one word per blank; each must become its option index.
    if "o" in kinds and nb > 1:
        vals = text_blanks(seg, nb)
        if not vals:
            problems.append((tag, "NB-O %d | %s" % (nb, seg[:70])))
            return None
        obj = {}
        for i, (vals_i, kind) in enumerate(zip(vals, kinds), 1):
            if kind != "o":
                obj[str(i)] = vals_i
                continue
            opts = [clean(x) for x in (ph[i - 1][2] or "").split("|")]
            idxs = []
            for cand in vals_i:
                j = option_index(cand, opts, tag)
                if j is None or j > len(opts):
                    problems.append((tag, "OPT-N? %s || %s" % (cand[:40], "|".join(opts)[:70])))
                    return None
                idxs.append(str(j))
            obj[str(i)] = idxs
        return obj

    # ---- plain text blanks -------------------------------------------------
    vals = text_blanks(seg, nb)
    if not vals:
        problems.append((tag, "NB %d | %s" % (nb, seg[:70])))
        return None
    if any(not v for v in vals):
        problems.append((tag, "EMPTYVAR"))
        return None
    return {str(i + 1): v for i, v in enumerate(vals)}


def try_cell(text, qs, tag, problems=None):
    """Score/encode one answer cell against one exercise."""
    sink = problems if problems is not None else []
    items, prefix = split_items(text)
    bynum = {}
    for q in qs:
        bynum.setdefault(printed_number(q), []).append(q)
    out = {}
    if items:
        # The book prints a whole table/cloze as one question with N blanks,
        # while the key numbers those blanks 1..N.  Merge them back.
        if (len(qs) == 1 and len(items) == len(qs[0]["ph"])
                and not any(p[0] == "T" for p in qs[0]["ph"])):
            merged = {}
            for i, n in enumerate(sorted(items), 1):
                v = variants(items[n]) or [clean(items[n])]
                merged[str(i)] = v
            out = {str(qs[0]["seq"]): merged}
        else:
            for n, seg in items.items():
                for q in bynum.get(n, []):
                    obj = encode(q, seg, sink, "%s q%d" % (tag, q["seq"]))
                    if obj:
                        out[str(q["seq"])] = obj
    else:
        usable = [q for q in qs if not any(p[0] == "T" for p in q["ph"])]
        need = sum(len(q["ph"]) for q in usable)
        pieces = split_flat(text)
        if need and len(pieces) == need:
            it = iter(pieces)
            for q in usable:
                obj = {}
                for i in range(len(q["ph"])):
                    piece = next(it)
                    obj[str(i + 1)] = variants(piece) or [piece]
                out[str(q["seq"])] = obj
        elif need:
            sink.append((tag, "FLAT? need %d got %d | %s"
                         % (need, len(pieces), text[:70])))
    return len(out), out


def align(page_cells, db_exs, tag):
    """Best order-preserving (cell -> exercise) matching by encoded count."""
    n, m = len(page_cells), len(db_exs)
    S = [[0] * m for _ in range(n)]
    A = {}
    for i in range(n):
        text = page_cells[i][1]
        cell_items, _ = split_items(text)
        tf_like = bool(cell_items) and all(
            re.fullmatch(r"[VF]\.?", clean(seg)) for seg in cell_items.values())
        arrow = "\u2192" in text
        for j in range(m):
            qs = db_exs[j][1]
            sc, out = try_cell(text, qs, tag)
            has_c = any(p[0] == "c" for q in qs for p in q["ph"])
            has_text = any(p[0] in "bL" for q in qs for p in q["ph"])
            # A pure V/F key can only belong to a tick exercise; a "V -> ..."
            # correction needs a text blank.  These break ties such as
            # ch1 p7 (ccb vs cc) that would otherwise silently swap keys.
            if sc:
                if tf_like and not has_c:
                    sc = 0
                elif arrow and not has_text:
                    sc = 0
                elif arrow and has_text:
                    sc += 2
            S[i][j] = sc
            A[(i, j)] = out
    dp = [[0] * (m + 1) for _ in range(n + 1)]
    bk = [[None] * (m + 1) for _ in range(n + 1)]
    for i in range(1, n + 1):
        for j in range(1, m + 1):
            v = dp[i - 1][j - 1] + S[i - 1][j - 1]
            b = "match"
            if dp[i - 1][j] > v:
                v, b = dp[i - 1][j], "skip-cell"
            if dp[i][j - 1] > v:
                v, b = dp[i][j - 1], "skip-ex"
            dp[i][j], bk[i][j] = v, b
    pairs, used_c, used_e = [], set(), set()
    i, j = n, m
    while i > 0 and j > 0:
        b = bk[i][j]
        if b == "match":
            if S[i - 1][j - 1] > 0:
                pairs.append((i - 1, j - 1))
                used_c.add(i - 1); used_e.add(j - 1)
            i -= 1; j -= 1
        elif b == "skip-cell":
            i -= 1
        else:
            j -= 1
    return list(reversed(pairs)), used_c, used_e, S, A


def main():
    cells = load_cells()
    spec = load_spec()
    answers = collections.defaultdict(lambda: collections.defaultdict(dict))
    problems = []
    stats = collections.Counter()

    for (ch, pg), exs in spec.items():
        tag0 = "ch%d p%d" % (ch, pg)
        page_cells = sorted([(ex, txt) for (c, p, ex), txt in cells.items()
                             if c == ch and p == pg])
        db_exs = sorted(exs.items(), key=lambda kv: min(q["seq"] for q in kv[1]))
        if not page_cells:
            stats["page-no-cell"] += 1
            continue
        pairs, used_c, used_e, S, A = align(page_cells, db_exs, tag0)
        for i, j in pairs:
            ordn = j + 1
            for qseq, obj in A[(i, j)].items():
                answers[ch][pg].setdefault(str(ordn), {})[qseq] = obj
                stats["mapped"] += 1
            try_cell(page_cells[i][1], db_exs[j][1], "%s ex%d" % (tag0, ordn), problems)
        for j in range(len(db_exs)):
            if j not in used_e:
                stats["ex-no-answer"] += 1
        for i in range(len(page_cells)):
            if i not in used_c:
                stats["cell-unused"] += 1
                problems.append((tag0, "CELL UNUSED ex%d | %s"
                                 % (page_cells[i][0], page_cells[i][1][:60])))

    for f in glob.glob(os.path.join(OUTDIR, "v*.json")):
        os.remove(f)
    total = 0
    for ch in sorted(answers):
        doc = {"pages": {str(pg): dict(v) for pg, v in answers[ch].items()}}
        with open(os.path.join(OUTDIR, "v%02d.json" % ch), "w", encoding="utf-8") as f:
            json.dump(doc, f, ensure_ascii=False, indent=1)
        total += sum(len(q) for exs in answers[ch].values() for q in exs.values())
    with open(os.path.join(OUTDIR, "_report.txt"), "w", encoding="utf-8") as f:
        f.write("tag | problem\n")
        for tag, msg in problems:
            f.write("%s | %s\n" % (tag, msg))
    print("files:", len(answers), "questions with answers:", total)
    for k, v in stats.most_common():
        print("  %-18s %d" % (k, v))
    kinds = collections.Counter(m.split()[0] for _, m in problems)
    print("problems:", len(problems), dict(kinds))


if __name__ == "__main__":
    main()
