"""Draws the SilverSpoon logo concepts; the app uses D (minimal mark): concept_d.png -> SilverSpoon.png/.ico.
icons (Raycast, Linear, Motrix, Downie): bold geometry, few colours."""
import math
import sys
from PIL import Image, ImageDraw, ImageFont, ImageFilter

S = 4
N = 1024 * S


def sc(v):
    return int(v * S)


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(len(a)))


def linear(size, c1, c2, angle=135):
    """Linear gradient at angle (degrees), via a rotated strip."""
    w, h = size
    d = int(math.hypot(w, h)) + 2
    strip = Image.new("RGBA", (d, 1))
    for i in range(d):
        strip.putpixel((i, 0), lerp(c1, c2, i / (d - 1)))
    g = strip.resize((d, d))
    g = g.rotate(-angle + 90 - 90, resample=Image.BICUBIC)
    left = (d - w) // 2
    return g.crop((left, left, left + w, left + h))


def tile_mask(pad=64, radius=228):
    m = Image.new("L", (N, N), 0)
    ImageDraw.Draw(m).rounded_rectangle((sc(pad), sc(pad), sc(1024 - pad), sc(1024 - pad)), radius=sc(radius), fill=255)
    return m


def stroke(d, pts, w, fill):
    """Polyline with round caps and joins."""
    pts = [(sc(x), sc(y)) for x, y in pts]
    d.line(pts, fill=fill, width=sc(w), joint="curve")
    r = sc(w) / 2
    for x, y in pts:
        d.ellipse((x - r, y - r, x + r, y + r), fill=fill)


def finish(img):
    return img.resize((1024, 1024), Image.LANCZOS)


# A — Line glyph on a vivid gradient tile (Raycast / Linear).
def concept_a():
    img = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    tile = linear((N, N), (124, 92, 255, 255), (56, 140, 255, 255), 135)
    img.paste(tile, (0, 0), tile_mask())
    g = Image.new("L", (N, N), 0)
    d = ImageDraw.Draw(g)
    # Spoon bowl outline.
    d.ellipse((sc(392), sc(196), sc(632), sc(456)), outline=255, width=sc(60))
    # Handle into arrow, then tray.
    stroke(d, [(512, 456), (512, 720)], 60, 255)
    stroke(d, [(400, 612), (512, 724), (624, 612)], 60, 255)
    stroke(d, [(300, 700), (300, 824), (724, 824), (724, 700)], 60, 255)
    white = Image.new("RGBA", (N, N), (255, 255, 255, 255))
    img.paste(white, (0, 0), g)
    return finish(img)


# B — Solid spoon whose bowl holds a cut-out arrow (Downie / Transmit).
def concept_b():
    img = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    tile = linear((N, N), (24, 27, 36, 255), (12, 14, 20, 255), 160)
    img.paste(tile, (0, 0), tile_mask())
    # Spoon in a silver-to-white flat gradient.
    m = Image.new("L", (N, N), 0)
    d = ImageDraw.Draw(m)
    d.ellipse((sc(332), sc(150), sc(692), sc(590)), fill=255)
    d.rounded_rectangle((sc(474), sc(540), sc(550), sc(888)), radius=sc(38), fill=255)
    # Arrow cut out of the bowl.
    cut = Image.new("L", (N, N), 0)
    dc = ImageDraw.Draw(cut)
    dc.rounded_rectangle((sc(482), sc(250), sc(542), sc(420)), radius=sc(14), fill=255)
    dc.polygon([(sc(400), sc(380)), (sc(624), sc(380)), (sc(512), sc(500))], fill=255)
    m = Image.composite(Image.new("L", (N, N), 0), m, cut)
    metal = linear((N, N), (255, 255, 255, 255), (168, 178, 198, 255), 120)
    img.paste(metal, (0, 0), m)
    # Accent glow under the bowl.
    glow = Image.new("RGBA", (N, N), (124, 140, 255, 0))
    ga = Image.new("L", (N, N), 0)
    ImageDraw.Draw(ga).ellipse((sc(300), sc(560), sc(724), sc(700)), fill=120)
    glow.putalpha(ga.filter(ImageFilter.GaussianBlur(sc(60))))
    under = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    under.paste(glow, (0, 0), tile_mask())
    img = Image.alpha_composite(under, img) if False else Image.alpha_composite(img, under)
    img.paste(metal, (0, 0), m)
    return finish(img)


# C — Round badge, tilted spoon with speed lines (Motrix / IDM).
def concept_c():
    img = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    circ = Image.new("L", (N, N), 0)
    ImageDraw.Draw(circ).ellipse((sc(72), sc(72), sc(952), sc(952)), fill=255)
    bg = linear((N, N), (34, 211, 166, 255), (52, 120, 246, 255), 135)
    img.paste(bg, (0, 0), circ)
    m = Image.new("L", (N, N), 0)
    d = ImageDraw.Draw(m)
    d.ellipse((sc(392), sc(170), sc(632), sc(470)), fill=255)
    d.rounded_rectangle((sc(486), sc(440), sc(538), sc(830)), radius=sc(26), fill=255)
    # Speed lines behind the handle.
    for dx, y0, y1 in ((-120, 520, 700), (-200, 560, 660), (120, 520, 700)):
        pass
    m = m.rotate(-38, resample=Image.BICUBIC, center=(N / 2 + sc(30), N / 2))
    lines = Image.new("L", (N, N), 0)
    dl = ImageDraw.Draw(lines)
    for x0, x1, y in ((250, 400, 330), (200, 380, 420), (270, 410, 510)):
        stroke(dl, [(x0, y), (x1, y)], 34, 200)
    img.paste(Image.new("RGBA", (N, N), (255, 255, 255, 255)), (0, 0), lines)
    img.paste(Image.new("RGBA", (N, N), (255, 255, 255, 255)), (0, 0), m)
    return finish(img)


# D — Minimal mark: a white "droplet" spoon with the arrow as negative space,
# on a single accent colour (Linear / Arc).
def concept_d():
    img = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    tile = linear((N, N), (99, 102, 241, 255), (79, 70, 229, 255), 150)
    img.paste(tile, (0, 0), tile_mask())
    m = Image.new("L", (N, N), 0)
    d = ImageDraw.Draw(m)
    d.ellipse((sc(300), sc(170), sc(724), sc(594)), fill=255)          # bowl
    d.rounded_rectangle((sc(470), sc(560), sc(554), sc(870)), radius=sc(42), fill=255)  # handle
    cut = Image.new("L", (N, N), 0)
    dc = ImageDraw.Draw(cut)
    stroke(dc, [(512, 268), (512, 470)], 64, 255)
    stroke(dc, [(420, 392), (512, 484), (604, 392)], 64, 255)
    m = Image.composite(Image.new("L", (N, N), 0), m, cut)
    img.paste(Image.new("RGBA", (N, N), (255, 255, 255, 255)), (0, 0), m)
    return finish(img)


out_dir = sys.argv[1]
concepts = [("A  Line glyph", concept_a()), ("B  Silver on graphite", concept_b()),
            ("C  Fast spoon", concept_c()), ("D  Minimal mark", concept_d())]
sheet = Image.new("RGBA", (4 * 300 + 50, 420), (18, 20, 26, 255))
try:
    font = ImageFont.truetype("C:/Windows/Fonts/seguisb.ttf", 22)
except OSError:
    font = ImageFont.load_default()
ds = ImageDraw.Draw(sheet)
for i, (name, im) in enumerate(concepts):
    im.save(f"{out_dir}/concept_{'abcd'[i]}.png")
    x = 25 + i * 300
    big = im.resize((240, 240), Image.LANCZOS)
    sheet.paste(big, (x + 10, 30), big)
    for j, s in enumerate((48, 32, 16)):
        sm = im.resize((s, s), Image.LANCZOS)
        sheet.paste(sm, (x + 40 + j * 70, 300 + (48 - s) // 2), sm)
    ds.text((x + 10, 370), name, fill=(220, 224, 232), font=font)
sheet.save(f"{out_dir}/concepts.png")
print("ok")
