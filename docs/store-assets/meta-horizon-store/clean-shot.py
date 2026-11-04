"""Make an emulator screenshot look like the Quest panel: paint over the
Android status bar's clock and icons and the gesture handle (a Quest panel
has neither) with the app's own background, sampled from the status bar's
empty middle. Nothing is scaled: the shot stays 2560x1440.

Usage: python clean-shot.py screenshots/01-home.png [...]
"""
import sys
from PIL import Image, ImageDraw

for path in sys.argv[1:]:
    im = Image.open(path).convert('RGB')
    w, h = im.size
    bg = im.getpixel((w // 2, 30))
    d = ImageDraw.Draw(im)
    d.rectangle((0, 0, 360, 64), fill=bg)             # clock
    d.rectangle((w - 420, 0, w, 64), fill=bg)         # signal, wifi, battery
    d.rectangle((w // 2 - 200, h - 52, w // 2 + 200, h), fill=bg)  # gesture handle
    im.save(path, optimize=True)
    print(path, im.size, 'bg', bg)
