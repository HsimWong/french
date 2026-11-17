import sys, os
from PIL import Image

SRC = r"C:/Users/WangXin/AppData/Local/Temp/corriges"
OUT = r"C:/Users/WangXin/AppData/Local/Temp/corriges/crops"
os.makedirs(OUT, exist_ok=True)

# usage: zoom.py page x0 y0 x1 y1 scale name
page = sys.argv[1]
x0, y0, x1, y1 = map(float, sys.argv[2:6])
x0, y0, x1, y1 = int(x0), int(y0), int(x1), int(y1)
scale = float(sys.argv[6])
name = sys.argv[7]

im = Image.open(os.path.join(SRC, page + ".jpg"))
c = im.crop((x0, y0, x1, y1))
c = c.resize((int(c.width * scale), int(c.height * scale)), Image.LANCZOS)
p = os.path.join(OUT, name + ".png")
c.save(p)
print(p, c.size)
