#!/usr/bin/env python3
"""Render page images from the two source PDFs.

The PDFs are pure scans with no text layer. Split single-page PDFs already live
in the directories recorded in tools/chapter_pages.json.

Usage:
  python tools/render.py hi g 12 13 14 15 --outdir DIR --dpi 200
      -> DIR/p12/page-0001.png ... (high DPI for visual transcription)
  python tools/render.py scan g 13 15 --dpi 110 --quality 72
      -> web/scans/grammaire/p13.jpg ...
  python tools/render.py scan-all v
      -> every exercise page of the vocabulary book
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
META = json.load(open(os.path.join(ROOT, "tools", "chapter_pages.json"), encoding="utf-8"))
CLI = r"C:\Users\WangXin\.workbuddy\external-packages\pdf-cli\workbuddy-pdf.exe"


def render_page(book, page, fmt, dpi, quality, tmpdir):
    cfg = META[book]
    pdf_no = page + cfg["pdf_offset"]
    src = os.path.join(cfg["pdf_dir"], "page-%04d.pdf" % pdf_no)
    if not os.path.isfile(src):
        raise FileNotFoundError(src)
    outdir = os.path.join(tmpdir, "p%d" % page)
    os.makedirs(outdir, exist_ok=True)
    cmd = [CLI, src, outdir, "-f", fmt, "--dpi", str(dpi), "-q"]
    if fmt == "jpg":
        cmd += ["--jpeg-quality", str(quality)]
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=180)
    if r.returncode != 0:
        raise RuntimeError("render failed p%d: %s %s" % (page, r.stdout, r.stderr))
    produced = os.path.join(outdir, "page-0001." + ("png" if fmt == "png" else "jpg"))
    if not os.path.isfile(produced):
        raise RuntimeError("no output for p%d in %s" % (page, outdir))
    return produced


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("mode", choices=["hi", "scan", "scan-all"])
    ap.add_argument("book", choices=["grammaire", "vocabulaire", "g", "v"])
    ap.add_argument("pages", nargs="*", type=int)
    ap.add_argument("--outdir")
    ap.add_argument("--dpi", type=int, default=200)
    ap.add_argument("--quality", type=int, default=72)
    ap.add_argument("--force", action="store_true")
    ap.add_argument("--include-lesson", action="store_true", help="scan-all: also render lesson pages")
    a = ap.parse_args()
    book = "grammaire" if a.book == "g" else "vocabulaire" if a.book == "v" else a.book
    cfg = META[book]
    pages = a.pages
    if a.mode == "scan-all":
        keys = ["exercise_pages"]
        if a.include_lesson:
            keys.append("lesson_pages")
        pages = sorted({p for c in cfg["chapters"] for k in keys for p in c[k]})
    if not pages:
        ap.error("no pages given")
    fmt = "png" if a.mode == "hi" else "jpg"
    if a.mode == "hi":
        outdir = a.outdir or os.path.join(tempfile.gettempdir(), "french-hi", book)
        os.makedirs(outdir, exist_ok=True)
    else:
        outdir = os.path.join(ROOT, cfg["scan_dir"])
        os.makedirs(outdir, exist_ok=True)

    done = 0
    with tempfile.TemporaryDirectory(prefix="french-render-") as tmp:
        for p in pages:
            dest = (os.path.join(outdir, "p%d" % p, "page-0001.png") if a.mode == "hi"
                    else os.path.join(outdir, "p%d.jpg" % p))
            if os.path.isfile(dest) and not a.force:
                done += 1
                continue
            produced = render_page(book, p, fmt, a.dpi, a.quality, tmp)
            if a.mode == "hi":
                target_dir = os.path.join(outdir, "p%d" % p)
                os.makedirs(target_dir, exist_ok=True)
                shutil.copyfile(produced, dest)
            else:
                shutil.copyfile(produced, dest)
            done += 1
            print("p%-4d -> %s (%d bytes)" % (p, dest, os.path.getsize(dest)), flush=True)
    print("done %d/%d pages, outdir=%s" % (done, len(pages), outdir))


if __name__ == "__main__":
    sys.exit(main())
