#!/usr/bin/env python3
"""Build the course-scoped French dictionary seed from Kaikki/Wiktionary.

Usage (from the repository root):
  python tools/build_lexicon.py --kaikki path/to/kaikki.org-dictionary-French-words.jsonl

The output data/lexicon_seed.json is embedded in the Go binary. Kaikki data is
derived from Wiktionary and remains CC BY-SA; the source and licence fields are
kept on every entry. No course text, exercise or scan is read or written.
"""

import argparse
import json
import re
import sys
from pathlib import Path

WORD = re.compile(r"[A-Za-zÀ-ÿŒœÆæ][A-Za-zÀ-ÿŒœÆæ’'\-]*")
WELLFORMED = re.compile(r"^(?:[A-Za-zÀ-ÿŒœÆæ’'\-]+(?:\s+|$)){1,5}$")

def phrase(text):
    """A 2–5 word French expression without markup or punctuation noise."""
    text = norm(re.sub(r"<[^>]+>", " ", text or ""))
    text = re.sub(r"[«»“”\"(),.;:!?…0-9]", " ", text)
    text = re.sub(r"\s+", " ", text).strip(" -'")
    n = len(text.split())
    return text if 2 <= n <= 5 and WELLFORMED.match(text) else ""

POS_LABEL = {
    "noun": "n.",
    "verb": "v.",
    "adj": "adj.",
    "adv": "adv.",
    "pron": "pron.",
    "prep": "prép.",
    "conj": "conj.",
    "det": "dét.",
    "num": "num.",
    "intj": "interj.",
    "phrase": "loc.",
}


def norm(s):
    return re.sub(r"\s+", " ", s.replace("’", "'").strip().lower())


def course_words(root):
    words = set()
    phrases = set()
    files = [root / "data/courses/chapter01.json", root / "data/courses/vocabulaire01.json"]
    files += sorted((root / "data/courses/chapters").glob("*.json"))
    for path in files:
        try:
            doc = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            continue
        chapters = []
        if isinstance(doc.get("chapter1"), dict):
            chapters.append(doc["chapter1"])
        chapters += [c for c in doc.get("chapters", []) if isinstance(c, dict)]
        for chapter in chapters:
            for sec in chapter.get("sections", []):
                if p := phrase(sec.get("title_fr", "")):
                    phrases.add(p)
            for block in chapter.get("blocks", []):
                text = re.sub(r"<[^>]+>", " ", block.get("body_html", ""))
                words.update(norm(w) for w in WORD.findall(text))
                for line in re.split(r"</div>|<br\s*/?>", text):
                    if p := phrase(line):
                        phrases.add(p)
            for ex in chapter.get("exercises", []):
                text = re.sub(r"<[^>]+>", " ", (ex.get("instruction_fr") or "") + " " + (ex.get("note_html") or ""))
                words.update(norm(w) for w in WORD.findall(text))
                for line in re.split(r"</div>|<br\s*/?>", text):
                    if p := phrase(line):
                        phrases.add(p)
    return words, phrases


def headword(rec, allow_phrase=False):
    lemma = rec.get("word", "")
    if rec.get("lang_code", "fr") != "fr" or not lemma:
        return ""
    if not allow_phrase and " " in lemma.strip():
        return ""
    return norm(lemma)


def surface(rec):
    """Database spelling: phrases use the typographic apostrophe l’été."""
    return norm(rec.get("word", "")).replace("'", "\u2019")


def form_feat(tags):
    t = set(tags or [])
    parts = []
    for tag, label in (("infinitive", "infinitif"), ("gerund", "gérondif"),
                       ("participle", "participe"), ("indicative", "indicatif"),
                       ("subjunctive", "subjonctif"), ("imperative", "impératif"),
                       ("conditional", "conditionnel")):
        if tag in t:
            parts.append(label)
    for tag, label in (("present", "présent"), ("imperfect", "imparfait"),
                       ("future", "futur"), ("past", "passé"), ("pluperfect", "plus-que-parfait")):
        if tag in t:
            parts.append(label)
            break
    for tag, label in (("first-person", "1"), ("second-person", "2"), ("third-person", "3")):
        if tag in t:
            parts.append(label)
            break
    if "singular" in t:
        parts.append("sg")
    elif "plural" in t:
        parts.append("pl")
    if "feminine" in t:
        parts.append("f.")
    elif "masculine" in t:
        parts.append("m.")
    return " ".join(parts) or "forme"


def collect_forms(forms):
    """Return a full paradigm, including joint rows like finis / finit."""
    out = {}
    for f in forms:
        raw = norm(f.get("form", "")).replace("'", "\u2019")
        tags = f.get("tags") or []
        if not raw or "table-tags" in tags or "inflection-template" in tags:
            continue
        if len(raw) > 64 or "multiword-construction" in tags:
            continue
        # French Wiktionary writes changes as finis/finit, mangé/mangée.
        # Some templates use a longer join; split those too.
        variants = [raw]
        if "/" in raw and " " not in raw:
            variants = [p for p in raw.split("/") if p]
        for v in variants:
            out.setdefault(v, form_feat(tags))
        if "/" in raw and " " in raw and not raw.startswith("être ") and not raw.startswith("avoir "):
            for v in raw.split("/"):
                v = v.strip()
                if v and len(v.split()) <= 3:
                    out.setdefault(v, form_feat(tags))
    return out


def is_lemma_record(rec):
    for sense in rec.get("senses") or []:
        if sense.get("form_of") or sense.get("alt_of"):
            return False
    return True


def g(rec, key, default=None):
    """Small, allocation-free accessors used in the hot scan loop."""
    v = rec.get(key)
    return default if v is None else v


def form_targets(rec):
    out = set()
    for sense in g(rec, "senses", ()):
        for key in ("form_of", "alt_of"):
            for fo in g(sense, key, ()):
                w = g(fo, "word")
                if w:
                    out.add(norm(w).replace("'", "\u2019"))
    return out


def read_kaikki(path, wanted, wanted_phrases=None):
    wanted_phrases = wanted_phrases or ()
    wanted = {w.replace("'", "\u2019") for w in wanted} | {w.replace("'", "\u2019") for w in wanted_phrases}
    entries = {}
    inflections = {}
    redirects = {}
    seen = 0
    last_report = 0
    with path.open(encoding="utf-8", errors="replace", buffering=1 << 20) as src:
        for line in src:
            if '\"lang_code\":\"fr\"' not in line and '\"lang_code\": \"fr\"' not in line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            seen += 1
            if seen - last_report >= 100000:
                last_report = seen
                print(f"  scanned {seen} French records", file=sys.stderr, flush=True)
            slot = norm(g(rec, "word", "")).replace("'", "\u2019")
            if not slot:
                continue
            senses = g(rec, "senses", ())
            # Keep reverse mappings only for forms the textbook uses; carrying
            # the other 300k unrelated redirects wastes memory and sort time.
            targets = form_targets(rec) if slot in wanted else set()
            if targets:
                redirects.setdefault(slot, set()).update(targets)
            related = slot in wanted or not targets and " " not in slot
            if not related:
                continue
            if len(slot.split()) > 5:
                continue
            pos = g(rec, "pos", "")
            key = (slot, pos)
            if key not in entries:
                gloss_en = ""
                for sense in senses:
                    gl = g(sense, "glosses")
                    if gl:
                        gloss_en = gl[0][:400]
                        break
                entries[key] = {
                    "lemma": slot, "display": g(rec, "word", slot),
                    "pos": POS_LABEL.get(pos, pos), "gloss_zh": "", "gloss_en": gloss_en,
                    "ipa": "", "audio_url": "", "source": "kaikki",
                    "license": "Wiktionary · CC BY-SA", "level": "",
                }
            item = entries[key]
            if not item["ipa"] or not item["audio_url"]:
                for snd in g(rec, "sounds", ()):
                    if not item["ipa"] and snd.get("ipa"):
                        item["ipa"] = snd["ipa"]
                    if not item["audio_url"] and (snd.get("mp3_url") or snd.get("ogg_url")):
                        item["audio_url"] = snd.get("mp3_url") or snd.get("ogg_url")
                    if item["ipa"] and item["audio_url"]:
                        break
            for form, feat in collect_forms(g(rec, "forms", ())).items():
                inflections.setdefault((slot, form), feat)
    print(f"  first pass: {len(entries)} entries, {len(inflections)} forms, {len(redirects)} redirects", flush=True)
    return entries, inflections, redirects, seen


def merge_local(root, entries):
    """Human-authored additions live in data/lexicon/lexicon_manual.json."""
    path = root / "data/lexicon/lexicon_manual.json"
    if not path.exists():
        return []
    doc = json.loads(path.read_text(encoding="utf-8"))
    forms = list(doc.get("forms", []))
    for item in doc.get("entries", []):
        key = (norm(item["lemma"]), item["pos"])
        current = entries.get(key, {})
        current.update(item)
        current.setdefault("source", "manual")
        current.setdefault("license", "本站编辑 · CC BY-SA")
        entries[key] = current
    return forms


def write_seed(root, entries, wanted, forms=None):
    entries = list(entries.values())
    wanted = set(wanted)
    for item in entries:
        if item["lemma"] in wanted and not item["gloss_zh"]:
            item["gloss_zh"] = ""
    # One entry per line keeps glossary corrections reviewable in Git.
    entries.sort(key=lambda e: e["lemma"])
    output = {"entries": entries, "forms": forms or []}
    out = root / "data" / "lexicon_seed.json"
    lines = ['{"entries":[']
    lines += [json.dumps(e, ensure_ascii=False) + ("," if i < len(entries) - 1 else "") for i, e in enumerate(entries)]
    lines.append('],"forms":[')
    for i, f in enumerate(forms):
        lines.append(json.dumps(f, ensure_ascii=False) + ("," if i < len(forms) - 1 else ""))
    lines.append("]}")
    out.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"wrote {out} entries={len(entries)} forms={len(forms or [])} course_terms={len(wanted)}")


def main(argv):
    root = Path.cwd()
    parser = argparse.ArgumentParser()
    parser.add_argument("--kaikki", type=Path, help="Kaikki French JSONL file")
    parser.add_argument("--entries-only", action="store_true", help="Skip the Kaikki import (manual entries only)")
    parser.add_argument("--full-second-pass", action="store_true",
                        help="Re-read the whole dump to pull extra paradigms (slow)")
    args = parser.parse_args(argv)
    wanted = course_words(root)
    entries = {}
    if not args.entries_only:
        if not args.kaikki or not args.kaikki.exists():
            parser.error("--kaikki is required unless --entries-only is used")
        words, phrases = wanted
        entries, inflections, redirects, scanned = read_kaikki(args.kaikki, words, phrases)
        print(f"scanned {scanned} French records")
        lemmas = {e["lemma"] for e in entries.values()}
        pending = list(words | phrases)
        while pending:
            form = pending.pop().replace("'", "\u2019")
            for target in redirects.get(form, ()):
                if target in lemmas or " " in target:
                    continue
                lemmas.add(target)
                pending.append(target)
                inflections.setdefault((target, form), "forme fléchie")
        # The redirect expansion is complete. A full second pass only adds
        # forms already visible in the first pass; the slow pass is opt-in
        # (tools/import_full_lexicon.py) so a normal build returns quickly.
        if lemmas and args.full_second_pass:
            with args.kaikki.open(encoding="utf-8", errors="replace") as src:
                for line in src:
                    try:
                        rec = json.loads(line)
                    except ValueError:
                        continue
                    if rec.get("lang_code", "fr") != "fr":
                        continue
                    lemma = surface(rec)
                    if lemma not in lemmas:
                        continue
                    pos = rec.get("pos", "")
                    key = (lemma, pos)
                    if key not in entries:
                        gloss_en = ""
                        for sense in rec.get("senses") or []:
                            if sense.get("glosses"):
                                gloss_en = sense["glosses"][0][:400]
                                break
                        entries[key] = {
                            "lemma": lemma, "display": rec.get("word", lemma),
                            "pos": POS_LABEL.get(pos, pos), "gloss_zh": "", "gloss_en": gloss_en,
                            "ipa": "", "audio_url": "", "source": "kaikki",
                            "license": "Wiktionary · CC BY-SA", "level": "",
                        }
                    item = entries[key]
                    if not item["gloss_en"]:
                        for sense in rec.get("senses") or []:
                            if sense.get("glosses"):
                                item["gloss_en"] = sense["glosses"][0][:400]
                                break
                    if not item["ipa"]:
                        for snd in rec.get("sounds") or []:
                            if snd.get("ipa"):
                                item["ipa"] = snd["ipa"]
                                break
                    for form, feat in collect_forms(rec.get("forms") or []).items():
                        inflections.setdefault((lemma, form), feat)
    else:
        words, phrases = wanted
    manual_forms = merge_local(root, entries)
    form_rows = []
    for e in entries.values():
        form_rows.append({"lemma": e["lemma"], "form": e["lemma"], "feat": "lemma", "source": e.get("source", "kaikki")})
    for (lemma, form), feat in sorted(inflections.items()):
        if lemma in entries_lookup(entries):
            form_rows.append({"lemma": lemma, "form": form.replace("’", "'"), "feat": feat, "source": "kaikki"})
    form_rows += manual_forms
    write_seed(root, entries, words | phrases, form_rows)
    return 0


def entries_lookup(entries):
    return {e["lemma"] for e in entries.values()}


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
