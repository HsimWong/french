import re, sys, os

D = r"C:/Users/WangXin/WorkBuddy/Français Prograssive/data/import/answers/raw"
pat_ch = re.compile(r"^@CH \S+$")
pat_page = re.compile(r"^@PAGE \d+$")
pat_ex = re.compile(r"^@EX \d+$")
pat_item = re.compile(r"^\d+\. \S")
pat_block = re.compile(r"^- .+")
pat_empty = re.compile(r"^@EMPTY$")

for n in range(14, 23):
    f = os.path.join(D, f"p{n:03d}.txt")
    raw = open(f, "rb").read()
    if raw.startswith(b"\xef\xbb\xbf"):
        print(f"BOM! {f}")
    lines = raw.decode("utf-8").splitlines()
    bad = []
    ell = 0
    for i, l in enumerate(lines, 1):
        ell += l.count("\u2026")
        if l.strip() == "":
            bad.append((i, "空行")); continue
        if not (pat_ch.match(l) or pat_page.match(l) or pat_ex.match(l)
                or pat_item.match(l) or pat_block.match(l) or pat_empty.match(l)):
            bad.append((i, l[:60]))
        if "..." in l:
            bad.append((i, "含 ASCII 三点"))
        if "\t" in l:
            bad.append((i, "含制表符"))
    pages = [l for l in lines if l.startswith("@PAGE")]
    exs = [l for l in lines if l.startswith("@EX")]
    print(f"p{n:03d}: 行数={len(lines)} 省略号={ell} 页组={len(pages)} 练习={len(exs)} 非法行={len(bad)}")
    for b in bad:
        print("   !", b)
