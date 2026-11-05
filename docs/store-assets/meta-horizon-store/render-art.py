"""Meta Horizon Store art for the Quest build, from the master icon art.

Sizes from Meta's asset guidelines (developers.meta.com/horizon/resources/
asset-guidelines): Hero 3000x900, Cover Landscape 2560x1440, Cover Square
1440x1440, Cover Portrait 1008x1440, Mini Landscape 1080x360, Icon 512x512
(opaque), Logo (transparent). The title must match the app name exactly
("OnScreen") and stay out of the bleed areas, the top and bottom 20% and
the left and right 10% (VRC.Quest.Asset.3); the hero's branding sits in
its centre, so a centre crop keeps it whole (VRC.Quest.Asset.4).
check_bleed() fails the run if a title strays.

Run: python render-art.py   (writes next to this file)
"""
import os
from PIL import Image, ImageDraw, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
MASTER = os.path.join(HERE, '..', 'master')
FONT = 'C:/Windows/Fonts/segoeuib.ttf'
TITLE = 'OnScreen'

art_full = Image.open(os.path.join(MASTER, 'icon-1024.png')).convert('RGB')
# The illustration's bounding box in the 1024 master (the rest is black).
ART = art_full.crop((42, 232, 995, 803))


def art(height):
    w = round(ART.width * height / ART.height)
    return ART.resize((w, height), Image.LANCZOS)


def font_for_height(cap_px):
    """A font whose capital 'O' is about cap_px tall."""
    size = cap_px
    while True:
        f = ImageFont.truetype(FONT, size)
        b = f.getbbox('O')
        if b[3] - b[1] >= cap_px:
            return f
        size += 2


def draw_title(img, cx, cy, cap_px, mode='RGB'):
    """Draw the title centred on (cx, cy); returns its box (l, t, r, b)."""
    d = ImageDraw.Draw(img)
    f = font_for_height(cap_px)
    b = d.textbbox((0, 0), TITLE, font=f)
    w, h = b[2] - b[0], b[3] - b[1]
    d.text((cx - w / 2 - b[0], cy - h / 2 - b[1]), TITLE, font=f, fill=(255, 255, 255, 255) if mode == 'RGBA' else (255, 255, 255))
    return (cx - w / 2, cy - h / 2, cx + w / 2, cy + h / 2)


def check_bleed(name, W, H, box, centre_crop=False):
    """Fail if the title is in a bleed area (VRC.Quest.Asset.3), or, for the
    hero, outside the middle 16:9 crop of its height (VRC.Quest.Asset.4)."""
    l, t, r, b = box
    assert l >= W * 0.10 and r <= W * 0.90, f'{name}: title in the side bleed ({l:.0f}-{r:.0f} of {W})'
    assert t >= H * 0.20 and b <= H * 0.80, f'{name}: title in the top/bottom bleed ({t:.0f}-{b:.0f} of {H})'
    if centre_crop:
        cw = H * 16 / 9
        assert l >= (W - cw) / 2 and r <= (W + cw) / 2, f'{name}: title outside the centre {cw:.0f}px'
    print(f'{name}: title x {l / W:.0%}-{r / W:.0%}, y {t / H:.0%}-{b / H:.0%}')


def side_by_side(W, H, art_h, cap, name, gap_frac=0.04, centre_crop=False):
    """Title left, art right, both on the vertical centre line, the pair
    centred."""
    img = Image.new('RGB', (W, H), (0, 0, 0))
    a = art(art_h)
    gap = W * gap_frac
    f = font_for_height(cap)
    tb = ImageDraw.Draw(img).textbbox((0, 0), TITLE, font=f)
    tw = tb[2] - tb[0]
    total = tw + gap + a.width
    x0 = (W - total) / 2
    box = draw_title(img, x0 + tw / 2, H / 2, cap)
    check_bleed(name, W, H, box, centre_crop)
    img.paste(a, (round(x0 + tw + gap), round((H - a.height) / 2)))
    img.save(os.path.join(HERE, name), optimize=True)


def stacked(W, H, art_w, cap, name):
    """Art above, title below, both inside the middle 60%."""
    img = Image.new('RGB', (W, H), (0, 0, 0))
    a = art(round(ART.height * art_w / ART.width))
    band_top, band_bottom = H * 0.22, H * 0.78
    block = a.height + cap * 2.2
    y = band_top + (band_bottom - band_top - block) / 2
    img.paste(a, (round((W - a.width) / 2), round(y)))
    box = draw_title(img, W / 2, y + a.height + cap * 1.3, cap)
    check_bleed(name, W, H, box)
    img.save(os.path.join(HERE, name), optimize=True)


# The hero is shown cropped on some surfaces: title and art packed into the
# middle 1600px (a 16:9 crop of its height).
side_by_side(3000, 900, 400, 110, 'hero-cover-3000x900.png', gap_frac=0.02, centre_crop=True)
side_by_side(2560, 1440, 560, 135, 'cover-landscape-2560x1440.png', gap_frac=0.035)
side_by_side(1080, 360, 190, 62, 'mini-landscape-1080x360.png')
stacked(1440, 1440, 980, 130, 'cover-square-1440x1440.png')
stacked(1008, 1440, 820, 110, 'cover-portrait-1008x1440.png')

# Icon: the master, opaque, square corners.
Image.open(os.path.join(MASTER, 'icon-512.png')).convert('RGB').save(os.path.join(HERE, 'icon-512x512.png'), optimize=True)

# Logo: the title alone on transparency (32-bit PNG).
f = font_for_height(300)
probe = ImageDraw.Draw(Image.new('RGBA', (10, 10)))
b = probe.textbbox((0, 0), TITLE, font=f)
pad = 40
logo = Image.new('RGBA', (b[2] - b[0] + pad * 2, b[3] - b[1] + pad * 2), (0, 0, 0, 0))
ImageDraw.Draw(logo).text((pad - b[0], pad - b[1]), TITLE, font=f, fill=(255, 255, 255, 255))
logo.save(os.path.join(HERE, 'logo-transparent.png'), optimize=True)

for n in sorted(os.listdir(HERE)):
    if n.endswith('.png'):
        im = Image.open(os.path.join(HERE, n))
        print(n, im.size, im.mode)
