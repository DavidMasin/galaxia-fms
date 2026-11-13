"""Draws the neutral Galaxia FMS logos that replace the 2026 game artwork inherited from Cheesy Arena.

The images keep the file names and pixel sizes of the ones they replace, so the pages and stylesheets that show
them need no change. Theming (milestone 9) will let an event swap in its own artwork.

Usage, from the repository root:
    python -I tools/brand/make_logos.py
"""

import math
import os
import sys

from PIL import Image, ImageDraw, ImageFont

NAVY = (20, 33, 92, 255)
BLUE = (44, 92, 197, 255)
ORANGE = (255, 152, 0, 255)
WHITE = (255, 255, 255, 255)

FONT_BOLD = "C:/Windows/Fonts/segoeuib.ttf"
FONT_REGULAR = "C:/Windows/Fonts/segoeui.ttf"
OUTPUT_DIR = os.path.join("static", "img")
SUPERSAMPLE = 3  # Draw large and scale down, for smooth edges.


def font(path, size):
    return ImageFont.truetype(path, int(size))


def draw_emblem(draw, cx, cy, radius):
    """A planet with a tilted orbit and a star: the Galaxia mark. The orbit reaches 1.75 radii either side."""
    canvas = draw._image

    # The orbit is drawn on its own layer and split in two, so the back half passes behind the planet.
    orbit_w, orbit_h = radius * 1.75, radius * 0.55
    size = int(orbit_w * 2.6)
    ring = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    rc = size / 2
    ImageDraw.Draw(ring).ellipse(
        (rc - orbit_w, rc - orbit_h, rc + orbit_w, rc + orbit_h), outline=ORANGE, width=int(radius * 0.12),
    )
    ring = ring.rotate(-18, resample=Image.BICUBIC)
    back = ring.crop((0, 0, size, int(rc)))
    front = ring.crop((0, int(rc), size, size))
    canvas.alpha_composite(back, (int(cx - rc), int(cy - rc)))

    # The planet: a lit crescent at the top left over a darker body.
    planet = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    planet_draw = ImageDraw.Draw(planet)
    planet_draw.ellipse((cx - radius, cy - radius, cx + radius, cy + radius), fill=BLUE)
    shadow = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    offset = radius * 0.22
    ImageDraw.Draw(shadow).ellipse(
        (cx - radius + offset, cy - radius + offset, cx + radius + offset, cy + radius + offset), fill=NAVY,
    )
    mask = Image.new("L", canvas.size, 0)
    ImageDraw.Draw(mask).ellipse((cx - radius, cy - radius, cx + radius, cy + radius), fill=255)
    planet.paste(shadow, (0, 0), Image.composite(shadow, Image.new("RGBA", canvas.size), mask).getchannel("A"))
    canvas.alpha_composite(planet)

    # The front half of the orbit, over the planet.
    canvas.alpha_composite(front, (int(cx - rc), int(cy)))

    # A four-pointed star above and to the right.
    sx, sy, s = cx + radius * 1.15, cy - radius * 1.15, radius * 0.34
    points = []
    for i in range(8):
        angle = math.pi / 4 * i - math.pi / 2
        length = s if i % 2 == 0 else s * 0.28
        points.append((sx + length * math.cos(angle), sy + length * math.sin(angle)))
    draw.polygon(points, fill=ORANGE)


def centered_text(draw, cx, y, text, typeface, fill):
    left, top, right, bottom = draw.textbbox((0, 0), text, font=typeface)
    draw.text((cx - (right - left) / 2 - left, y - top), text, font=typeface, fill=fill)
    return bottom - top


def emblem_with_name(width, height):
    """The emblem with the name beneath it, for the portrait and landscape logo slots."""
    w, h = width * SUPERSAMPLE, height * SUPERSAMPLE
    image = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)
    unit = min(w, h * 0.78)
    radius = unit * 0.24
    cy = h * 0.5 - unit * 0.16
    draw_emblem(draw, w / 2, cy, radius)
    name_size = unit * 0.2
    y = cy + radius * 1.5
    y += centered_text(draw, w / 2, y, "GALAXIA", font(FONT_BOLD, name_size), NAVY) + name_size * 0.25
    centered_text(draw, w / 2, y, "FIELD MANAGEMENT", font(FONT_REGULAR, name_size * 0.36), BLUE)
    return image.resize((width, height), Image.LANCZOS)


def wordmark(width, height):
    """The emblem beside the name, for the wide header used in printed reports."""
    w, h = width * SUPERSAMPLE, height * SUPERSAMPLE
    image = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)
    radius = h * 0.26
    cx = radius * 1.95
    draw_emblem(draw, cx, h * 0.55, radius)
    left = cx + radius * 2.1
    available = w - left - h * 0.06
    name_size = h * 0.42
    typeface = font(FONT_BOLD, name_size)
    text_left, top, text_right, bottom = draw.textbbox((0, 0), "GALAXIA FMS", font=typeface)
    if text_right - text_left > available:
        name_size *= available / (text_right - text_left)
        typeface = font(FONT_BOLD, name_size)
        text_left, top, text_right, bottom = draw.textbbox((0, 0), "GALAXIA FMS", font=typeface)
    draw.text((left - text_left, h * 0.55 - (bottom - top) / 2 - top), "GALAXIA FMS", font=typeface, fill=NAVY)
    return image.resize((width, height), Image.LANCZOS)


def main():
    if not os.path.isdir(OUTPUT_DIR):
        sys.exit("Run this from the repository root.")
    images = {
        "blinds-logo.png": emblem_with_name(995, 1443),
        "alliance-station-logo.png": emblem_with_name(995, 1443),
        "lower-third-logo.png": emblem_with_name(959, 720),
        "game-logo.png": wordmark(2343, 922),
    }
    for name, image in images.items():
        path = os.path.join(OUTPUT_DIR, name)
        image.save(path, optimize=True)
        print("wrote", path, image.size)


if __name__ == "__main__":
    main()
