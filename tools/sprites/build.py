#!/usr/bin/env python3
"""Конвертер спрайт-листов маскота Relay в пиксельные кадры для TUI.

Вход:  assets/sheets/{green,purple,red}.png (1254x1254, одинаковая раскладка)
Выход: internal/tui/sprites/{small,medium,hard,maximum}.json
       assets/sprites/preview_<theme>.png  (увеличенное превью для проверки)

Кадр это сетка пикселей W x H (H чётная). В терминале два пикселя по вертикали
рисуются одной ячейкой символом "▀" (верх = цвет текста, низ = цвет фона), поэтому
пиксели квадратные и чёткие. Прозрачные пиксели ('.') показывают фон карточки.

Темы:  small = зелёный, medium = фиолетовый, hard = красный,
       maximum = радужный (зелёный лист; акцентные цвета палитры помечены в "accent",
       а оттенок им подставляет рантайм и плавно вращает).

Зависимости: Pillow, numpy, scipy не нужен.   Запуск: python3 tools/sprites/build.py
"""
import colorsys
import json
import os
import sys
from collections import deque

import numpy as np
from PIL import Image, ImageFilter

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SHEETS = os.path.join(ROOT, "assets", "sheets")
OUT = os.path.join(ROOT, "internal", "tui", "sprites")
PREVIEW = os.path.join(ROOT, "assets", "sprites")

# центры эмоций по x в листе; окно кропа одинаковое для всех кадров
EMOTIONS = {
    "calm": 140,
    "joy": 358,
    "surprise": 582,
    "noticed": 852,
    "tired": 1085,
}
CROP = (-90, 676, 112, 838)      # (dx0, y0, dx1, y1) относительно центра эмоции
SWATCH_Y = 92
SWATCH_X = [908, 961, 1014, 1066, 1118, 1171]

OUT_W = 44                       # ширина кадра в пикселях (= в ячейках терминала)
N_COLORS = 30                    # размер палитры темы
BG_TOL = 11                      # допуск цвета фона листа при заливке от краёв
CHARS = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

THEMES = {"small": "green", "medium": "purple", "hard": "red", "maximum": "green"}


def sheet_bg(im):
    """Цвет фона листа: медиана по тихим участкам у левого края."""
    a = np.asarray(im).astype(int)
    patch = np.concatenate([a[700:760, 10:30].reshape(-1, 3), a[860:900, 400:420].reshape(-1, 3)])
    return np.median(patch, axis=0)


def background_mask(arr, bg):
    """True для пикселей фона: заливка от краёв по цвету, близкому к фону.
    Чёрные внутренние области (капюшон, одежда) заливкой не затрагиваются,
    пока они отделены от краёв более светлыми пикселями."""
    h, w, _ = arr.shape
    near = np.abs(arr - bg).max(axis=2) <= BG_TOL
    seen = np.zeros((h, w), dtype=bool)
    q = deque()
    for x in range(w):
        for y in (0, h - 1):
            if near[y, x] and not seen[y, x]:
                seen[y, x] = True
                q.append((y, x))
    for y in range(h):
        for x in (0, w - 1):
            if near[y, x] and not seen[y, x]:
                seen[y, x] = True
                q.append((y, x))
    while q:
        y, x = q.popleft()
        for dy, dx in ((1, 0), (-1, 0), (0, 1), (0, -1)):
            ny, nx = y + dy, x + dx
            if 0 <= ny < h and 0 <= nx < w and near[ny, nx] and not seen[ny, nx]:
                seen[ny, nx] = True
                q.append((ny, nx))
    return seen


def crop_frame(im, cx, bg):
    dx0, y0, dx1, y1 = CROP
    c = im.crop((cx + dx0, y0, cx + dx1, y1))
    h = round(OUT_W * c.height / c.width)
    h += h % 2
    arr = np.asarray(c).astype(float)
    bgm = background_mask(arr, bg).astype(float)
    sharp = c.filter(ImageFilter.UnsharpMask(radius=1.2, percent=60, threshold=2))
    small = np.asarray(sharp.resize((OUT_W, h), Image.BOX)).astype(float)
    cover = np.asarray(Image.fromarray((bgm * 255).astype(np.uint8)).resize((OUT_W, h), Image.BOX)).astype(float) / 255
    transparent = cover > 0.55
    return small, transparent


def hex_of(rgb):
    return "#%02x%02x%02x" % tuple(int(round(v)) for v in rgb)


def build_theme(sheet_name, rainbow):
    im = Image.open(os.path.join(SHEETS, sheet_name + ".png")).convert("RGB")
    bg = sheet_bg(im)
    frames = {}
    for name, cx in EMOTIONS.items():
        frames[name] = crop_frame(im, cx, bg)

    # единая палитра темы по непрозрачным пикселям всех кадров
    px = np.concatenate([s[~t] for s, t in frames.values()])
    strip = Image.new("RGB", (len(px), 1))
    strip.putdata([tuple(int(round(v)) for v in p) for p in px])
    q = strip.quantize(colors=N_COLORS, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    pal_flat = q.getpalette()[: N_COLORS * 3]

    # привязка цвета каждого пикселя к ближайшему элементу палитры
    pal = np.array(pal_flat, dtype=float).reshape(-1, 3)
    used, palette = {}, []

    def index_of(rgb):
        d = ((pal - rgb) ** 2).sum(axis=1)
        k = int(d.argmin())
        if k not in used:
            used[k] = len(palette)
            palette.append(tuple(pal[k]))
        return used[k]

    out_frames = {}
    for name, (small, transparent) in frames.items():
        rows = []
        for y in range(small.shape[0]):
            row = ""
            for x in range(small.shape[1]):
                row += "." if transparent[y, x] else CHARS[index_of(small[y, x])]
            rows.append(row)
        out_frames[name] = {"w": OUT_W, "h": small.shape[0], "px": rows}

    accent = []
    if rainbow:
        for i, (r, g, b) in enumerate(palette):
            h, s, v = colorsys.rgb_to_hsv(r / 255, g / 255, b / 255)
            if 0.18 < h < 0.45 and s > 0.35 and v > 0.25:
                accent.append(i)

    return {
        "palette": [hex_of(c) for c in palette],
        "accent": accent,
        "swatch": [hex_of(im.getpixel((x, SWATCH_Y))) for x in SWATCH_X],
        "rainbow": rainbow,
        "frames": out_frames,
    }


def preview(theme, data, card=(24, 33, 28)):
    scale = 9
    names = list(data["frames"].keys())
    fw = OUT_W * scale + 12
    fh = max(f["h"] for f in data["frames"].values()) * scale + 12
    img = Image.new("RGB", (fw * len(names), fh), (12, 20, 16))
    px = img.load()
    pal = [tuple(int(c[i:i + 2], 16) for i in (1, 3, 5)) for c in data["palette"]]
    accent = set(data["accent"])

    def tint(idx, x, y):
        c = pal[idx]
        if idx in accent:
            _, s, v = colorsys.rgb_to_hsv(*(k / 255 for k in c))
            h = (x * 0.028 + y * 0.045) % 1.0
            return tuple(int(k * 255) for k in colorsys.hsv_to_rgb(h, max(s, 0.75), v))
        return c

    for n, name in enumerate(names):
        fr = data["frames"][name]
        ox, oy = n * fw + 6, 6
        for y in range(fr["h"]):
            for x in range(fr["w"]):
                ch = fr["px"][y][x]
                col = card if ch == "." else tint(CHARS.index(ch), x, y)
                for dy in range(scale):
                    for dx in range(scale):
                        px[ox + x * scale + dx, oy + y * scale + dy] = col
    os.makedirs(PREVIEW, exist_ok=True)
    img.save(os.path.join(PREVIEW, "preview_%s.png" % theme))


def main():
    os.makedirs(OUT, exist_ok=True)
    for theme, sheet in THEMES.items():
        data = build_theme(sheet, rainbow=(theme == "maximum"))
        with open(os.path.join(OUT, theme + ".json"), "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, separators=(",", ":"))
        preview(theme, data)
        print("%-8s <- %-6s colors=%d accent=%d frames=%d size=%dx%d" % (
            theme, sheet, len(data["palette"]), len(data["accent"]), len(data["frames"]),
            OUT_W, next(iter(data["frames"].values()))["h"]))


if __name__ == "__main__":
    sys.exit(main())
