#!/usr/bin/env python3
"""Emit per-chapter task packs for the questions the parser could not fill.

Each pack lists, for one grammar chapter, every still-unanswered question with
its page / exercise order / question seq / number of blanks and the stem text,
plus the Corrigés page images that carry the answers.

The agent fills data/import/answers/over/gNN.json in the same shape as the parsed
files; tools/parse_corriges.py merges those overrides last and re-validates the
blank count, so a bad guess is rejected instead of stored.

Usage: python tools/make_tasks.py [chapters...]
"""
import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ANS = os.path.join(ROOT, "data", "import", "answers")
TASKS = os.path.join(ANS, "tasks")
LABELS = ("Réponses possibles", "Réponse possible", "Exemples", "Exemple",
          "Questions possibles", "Autres possibilités", "Plusieurs réponses",
          "Suggestions", "Corrigé", "Autres exemples")

PH = re.compile(r"\{\{([bLTco])(!?)(?::([^}]*))?\}\}")
TAGS = re.compile(r"<[^>]+>")
CH_RE = re.compile(r"^@CH\s+(\d+)")
PAGE_RE = re.compile(r"^@PAGE\s+(\d+)")
EX_RE = re.compile(r"^@EX\s+(\d+)")


def stem_text(stem):
    """Render the stem with numbered blank markers."""
    n = [0]

    def rep(m):
        n[0] += 1
        opts = m.group(3)
        return "〖%d〗" % n[0] + ("(%s)" % opts if opts and m.group(1) == "o" else "")

    s = PH.sub(rep, stem or "")
    s = s.replace("</p>", " ").replace("<br/>", " ").replace("<br>", " ")
    s = TAGS.sub("", s)
    s = re.sub(r"\s+", " ", s).strip()
    return s


def cell_pages():
    """(ch,page,ex) -> sorted list of raw file names (PDF page stems)."""
    m = {}
    for path in sorted(glob.glob(os.path.join(ANS, "raw", "p*.txt"))):
        base = os.path.basename(path)[:-4]
        if base == "p051":
            continue
        ch = page = ex = None
        for line in open(path, encoding="utf-8"):
            line = line.strip()
            mm = CH_RE.match(line)
            if mm:
                ch, page, ex = int(mm.group(1)), None, None
                continue
            mm = PAGE_RE.match(line)
            if mm:
                page, ex = int(mm.group(1)), None
                continue
            mm = EX_RE.match(line)
            if mm:
                ex = int(mm.group(1))
                continue
            if ch is None or page is None or ex is None or line.startswith("@") or line.startswith("-"):
                continue
            if re.match(r"^\d{1,2}\.", line):
                m.setdefault((ch, page, ex), set()).add(base)
    return {k: sorted(v) for k, v in m.items()}


def main():
    spec = json.load(open(os.path.join(ANS, "_spec.json"), encoding="utf-8"))
    done = {}
    for f in glob.glob(os.path.join(ANS, "g*.json")):
        ch = int(os.path.basename(f)[1:3])
        d = json.load(open(f, encoding="utf-8"))
        for pg, pm in d["pages"].items():
            for ex, qm in pm.items():
                for seq in qm:
                    done[(ch, int(pg), int(ex), int(seq))] = 1
    pdfpages = cell_pages()

    want = [int(x) for x in sys.argv[1:]] or None
    os.makedirs(TASKS, exist_ok=True)
    total = 0
    for chs in sorted(spec, key=int):
        ch = int(chs)
        if want and ch not in want:
            continue
        lines, npages = [], set()
        for pg in sorted(spec[chs]["pages"], key=int):
            exs = spec[chs]["pages"][pg]
            for i, ex in enumerate(exs):
                miss = [q for q in ex["q"]
                        if q["nb"] > 0 and (ch, int(pg), i + 1, q["seq"]) not in done]
                if not miss:
                    continue
                src = pdfpages.get((ch, int(pg), i + 1), [])
                pages = ", ".join(p.replace("p", "") for p in src) or "?"
                lines.append("\n## page %s · exercice n°%d sur cette page (答案见 Corrigés PDF 第 %s 页)"
                             % (pg, i + 1, pages))
                for q in miss:
                    lines.append("- seq %d · %d blanc(s) · %s"
                                 % (q["seq"], q["nb"], stem_text(q["stem"])))
                npages.update(src)
        if not lines:
            continue
        total += len([l for l in lines if l.startswith("- seq")])
        head = ["# 任务：补第 %d 章 %s" % (ch, spec[chs]["title"]),
                "",
                "数据源：Corrigés 答案页 `C:/Users/WangXin/AppData/Local/Temp/corriges/pNNN.jpg`，",
                "本章涉及的页：%s" % ", ".join(sorted(p.replace("p", "") for p in npages)),
                "",
                "输出文件：`data/import/answers/over/g%02d.json`，结构：" % ch,
                "```json",
                '{"pages": {"<主书页>": {"<该页第几道大题>": {"<小题 seq>": "<单个空>=1 空时用字符串; 多空用字符串数组>"}}}}',
                "```",
                "",
                "规则：",
                "1. 只填下面列出的题，答案必须来自答案页原文，不要自己编。",
                "2. 空数必须与 `N blanc(s)` 一致；题干中的 〖k〗 就是第 k 空的位置。",
                "3. 答案只写空里该填的词/短语，不要带题干已有的词，不要带句末标点。",
                "4. 若答案页给出两个可接受答案（如 `le/la`、`Chaque … Chacun`），用 `|` 连接，如 `le|la`。",
                "5. 若答案页对该题只给自由范文（`Réponses possibles`/`Exemples`）或确实找不到，就**不要写这道题**。",
                ""]
        path = os.path.join(TASKS, "ch%02d.md" % ch)
        with open(path, "w", encoding="utf-8") as f:
            f.write("\n".join(head + lines) + "\n")
        print("ch%02d.md: %d questions" % (ch, len([l for l in lines if l.startswith("- seq")])))
    print("total questions in packs:", total)


if __name__ == "__main__":
    main()
