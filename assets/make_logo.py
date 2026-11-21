"""Draws the SilverSpoon app icon: a silver spoon whose handle becomes a
download arrow landing in a tray, on an indigo-violet squircle."""
import sys
from PIL import Image, ImageDraw, ImageFilter, ImageChops

S = 4  # supersampling
N = 1024 * S


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(len(a)))


def gradient(size, c1, c2, direction="diag"):
    w, h = size
    g = Image.new("RGBA", size)
    px = g.load()
    for y in range(h):
        for x in range(0, w):
            if direction == "diag":
                t = (x / w * 0.45 + y / h * 0.55)
            elif direction == "h":
                t = x / w
            else:
                t = y / h
            px[x, y] = lerp(c1, c2, t)
    return g


def grad_fast(size, stops, direction="v"):
    """Linear gradient via a 1-pixel strip, resized (fast)."""
    w, h = size
    n = 256
    strip = Image.new("RGBA", (1, n) if direction == "v" else (n, 1))
    for i in range(n):
        t = i / (n - 1)
        for j in range(len(stops) - 1):
            p0, c0 = stops[j]
            p1, c1 = stops[j + 1]
            if p0 <= t <= p1:
                c = lerp(c0, c1, (t - p0) / (p1 - p0) if p1 > p0 else 0)
                break
        strip.putpixel((0, i) if direction == "v" else (i, 0), c)
    return strip.resize(size, Image.BICUBIC)


def mask_from(draw_fn):
    m = Image.new("L", (N, N), 0)
    draw_fn(ImageDraw.Draw(m))
    return m


def sc(*v):
    return [x * S for x in v]


img = Image.new("RGBA", (N, N), (0, 0, 0, 0))

# --- Tile -------------------------------------------------------------------
pad, rad = 56, 232
tile_mask = mask_from(lambda d: d.rounded_rectangle(sc(pad, pad, 1024 - pad, 1024 - pad), radius=rad * S, fill=255))
tile = grad_fast((N, N), [(0, (99, 118, 255, 255)), (0.55, (110, 92, 246, 255)), (1, (139, 76, 226, 255))], "v")
# diagonal tint: blend a horizontal gradient for depth
tint = grad_fast((N, N), [(0, (255, 255, 255, 18)), (1, (0, 0, 0, 30))], "h")
tile = Image.alpha_composite(tile, tint)
# soft top glow
glow = Image.new("RGBA", (N, N), (0, 0, 0, 0))
ImageDraw.Draw(glow).rounded_rectangle(sc(pad, pad, 1024 - pad, 470), radius=rad * S, fill=(255, 255, 255, 34))
glow = glow.filter(ImageFilter.GaussianBlur(90 * S))
tile = Image.alpha_composite(tile, glow)
img.paste(tile, (0, 0), tile_mask)

# Thin inner border highlight.
border = Image.new("RGBA", (N, N), (0, 0, 0, 0))
ImageDraw.Draw(border).rounded_rectangle(sc(pad + 3, pad + 3, 1024 - pad - 3, 1024 - pad - 3), radius=(rad - 3) * S,
                                          outline=(255, 255, 255, 60), width=4 * S)
img = Image.alpha_composite(img, border)

# --- Spoon + arrow + tray shape -------------------------------------------------
cx = 512


def shape(d, inset=0):
    i = inset
    # bowl
    d.ellipse(sc(cx - 132 + i, 168 + i, cx + 132 - i, 498 - i), fill=255)
    # neck/handle tapering down
    d.polygon([tuple(sc(cx - 30 + i, 470)), tuple(sc(cx + 30 - i, 470)),
               tuple(sc(cx + 40 - i, 640)), tuple(sc(cx - 40 + i, 640))], fill=255)
    # arrow head
    d.polygon([tuple(sc(cx - 128 + i * 2, 612 + i)), tuple(sc(cx + 128 - i * 2, 612 + i)),
               tuple(sc(cx, 772 - i * 2))], fill=255)
    # tray
    d.rounded_rectangle(sc(cx - 232 + i, 812 + i, cx + 232 - i, 862 - i), radius=25 * S, fill=255)
    d.rounded_rectangle(sc(cx - 232 + i, 720 + i, cx - 182 - i, 862 - i), radius=25 * S, fill=255)
    d.rounded_rectangle(sc(cx + 182 + i, 720 + i, cx + 232 - i, 862 - i), radius=25 * S, fill=255)


spoon_mask = mask_from(shape)

# Drop shadow.
shadow = Image.new("RGBA", (N, N), (20, 10, 60, 0))
sh_alpha = spoon_mask.filter(ImageFilter.GaussianBlur(18 * S)).point(lambda v: int(v * 0.55))
shadow.putalpha(sh_alpha)
shadow = ImageChops.offset(shadow, 0, 16 * S)
img = Image.alpha_composite(img, shadow)

# Silver body: vertical metallic gradient with a bright band.
silver = grad_fast((N, N), [
    (0.00, (255, 255, 255, 255)),
    (0.22, (236, 240, 248, 255)),
    (0.42, (196, 204, 220, 255)),
    (0.55, (240, 243, 250, 255)),
    (0.75, (186, 194, 212, 255)),
    (1.00, (226, 231, 241, 255)),
], "v")
# horizontal shading for roundness
side = grad_fast((N, N), [(0, (70, 60, 140, 70)), (0.45, (255, 255, 255, 0)), (0.6, (255, 255, 255, 0)), (1, (70, 60, 140, 80))], "h")
silver = Image.alpha_composite(silver, side)
layer = Image.new("RGBA", (N, N), (0, 0, 0, 0))
layer.paste(silver, (0, 0), spoon_mask)
img = Image.alpha_composite(img, layer)

# Bowl inner shading (the dip of the spoon) and a specular highlight.
inner = Image.new("RGBA", (N, N), (0, 0, 0, 0))
di = ImageDraw.Draw(inner)
di.ellipse(sc(cx - 100, 200, cx + 100, 466), fill=(120, 110, 190, 70))
inner = inner.filter(ImageFilter.GaussianBlur(10 * S))
img = Image.alpha_composite(img, inner)
spec = Image.new("RGBA", (N, N), (0, 0, 0, 0))
# A thin crescent of light along the bowl's upper-left rim.
ds = ImageDraw.Draw(spec)
ds.arc(sc(cx - 112, 190, cx + 112, 476), start=150, end=250, fill=(255, 255, 255, 230), width=14 * S)
spec = spec.filter(ImageFilter.GaussianBlur(5 * S))
img = Image.alpha_composite(img, spec)

# Crisp outline for definition at small sizes.
edge = spoon_mask.filter(ImageFilter.FIND_EDGES).filter(ImageFilter.MaxFilter(3))
outline = Image.new("RGBA", (N, N), (70, 58, 160, 0))
outline.putalpha(edge.point(lambda v: int(v * 0.35)))
img = Image.alpha_composite(img, outline)

out = img.resize((1024, 1024), Image.LANCZOS)
png, ico = sys.argv[1], sys.argv[2]
out.save(png, optimize=True)
out.save(ico, sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
out.resize((256, 256), Image.LANCZOS).save(png.replace(".png", "_256.png"))
print("ok")
