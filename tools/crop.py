import sys, os
from PIL import Image

SRC = r"C:/Users/WangXin/AppData/Local/Temp/corriges"
OUT = r"C:/Users/WangXin/AppData/Local/Temp/corriges/crops"
os.makedirs(OUT, exist_ok=True)

page = sys.argv[1]          # e.g. p014
n = int(sys.argv[2]) if len(sys.argv) > 2 else 3
scale = float(sys.argv[3]) if len(sys.argv) > 3 else 2.0

im = Image.open(os.path.join(SRC, page + ".jpg"))
W, H = im.size
# ignore scan margins
top, bot = int(H * 0.05), int(H * 0.96)
step = (bot - top) / n
overlap = int(step * 0.10)
for i in range(n):
    y0 = max(0, int(top + i * step - overlap))
    y1 = min(H, int(top + (i + 1) * step + overlap))
    c = im.crop((0, y0, W, y1))
    c = c.resize((int(c.width * scale), int(c.height * scale)), Image.LANCZOS)
    p = os.path.join(OUT, f"{page}_b{i+1}.png")
    c.save(p)
    print(p, c.size)
