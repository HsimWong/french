#!/usr/bin/env python3
"""Download the Kaikki French dictionary (565 MB JSONL) to data/source.

Kaikki.org publishes a post-processed JSONL with every French word sense,
pronunciation and inflected form. Keeping the download outside the repository
lets tools/build_lexicon.py regenerate data/lexicon_seed.json deterministically.
The file is CC BY-SA (derived from English Wiktionary).
"""

import argparse
import sys
import urllib.request
from pathlib import Path

URL = "https://kaikki.org/dictionary/French/kaikki.org-dictionary-French.jsonl"


def main(argv):
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", type=Path, default=Path("data/source/kaikki.org-dictionary-French.jsonl"))
    ap.add_argument("--force", action="store_true")
    args = ap.parse_args(argv)
    if args.out.exists() and args.out.stat().st_size > 500_000_000 and not args.force:
        print(f"already present: {args.out} ({args.out.stat().st_size} bytes)")
        return 0
    args.out.parent.mkdir(parents=True, exist_ok=True)
    tmp = args.out.with_suffix(args.out.suffix + ".part")
    req = urllib.request.Request(URL, headers={"User-Agent": "FrancaisProgressif/lexicon-import"})
    with urllib.request.urlopen(req, timeout=120) as src, tmp.open("wb") as dst:
        total = int(src.headers.get("Content-Length") or 0)
        done = 0
        while True:
            chunk = src.read(8 << 20)
            if not chunk:
                break
            dst.write(chunk)
            done += len(chunk)
            if total:
                print(f"\r{done * 100 // total:3d}%  {done / 1e6:7.1f} / {total / 1e6:.0f} MB", end="")
            else:
                print(f"\r{done / 1e6:7.1f} MB", end="")
    print()
    tmp.replace(args.out)
    print(f"saved {args.out} ({args.out.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
