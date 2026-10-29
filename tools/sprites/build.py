#!/usr/bin/env python3
"""Конвертер спрайт-листов маскота Relay в терминальные кадры.

Вход:  assets/sheets/{green,purple,red}.png (1254x1254, одинаковая раскладка)
Выход: internal/tui/sprites/{small,medium,hard,maximum}.json
       assets/sprites/preview_<theme>.png  (увеличенное превью для проверки)

Каждая ячейка терминала кодирует блок 2x4 пикселя символом Брайля (U+2800..U+28FF):
точки рисуются цветом fg, пустое место цветом bg. Это в 4 раза детальнее полублоков.

Темы:  small = зелёный, medium = фиолетовый, hard = красный,
       maximum = радужный (берётся зелёный лист; акцентные цвета палитры помечены
       в поле "accent", а оттенок им подставляет рантайм и плавно вращает).

Зависимости: Pillow, numpy.   Запуск: python3 tools/sprites/build.py
"""
import colorsys
import json
import os
import sys

import numpy as np
from PIL import Image

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SHEETS = os.path.join(ROOT, "assets", "sheets")
OUT = os.path.join(ROOT, "internal", "tui", "sprites")
PREVIEW = os.path.join(ROOT, "assets", "sprites")

# имя кадра: (центр x, верх y, низ y, ширина кропа) в пикселях исходного листа
EMOTIONS = {
    "calm":     (140, 668, 852, 210),
    "joy":      (358, 668, 852, 210),
    "surprise": (582, 668, 852, 210),
    "noticed":  (852, 668, 852, 210),
    "tired":    (1085, 668, 852, 210),
}
ACTIONS = {
    "walk":   (132, 940, 1137, 150),
    "run":    (295, 940, 1137, 165),
    "jump":   (455, 940, 1137, 150),
    "link":   (632, 940, 1137, 185),
    "summon": (802, 940, 1137, 165),
}
SWATCH_Y = 92
SWATCH_X = [908, 961, 1014, 1066, 1118, 1171]

COLS = 36                  # ширина спрайта в ячейках терминала
DARK_LUM = 26              # темнее этого считаем фоном (прозрачным)
N_COLORS = 48              # размер палитры темы
CHARS = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
BITS = [[0x01, 0x08], [0x02, 0x10], [0x04, 0x20], [0x40, 0x80]]  # [строка][столбец]

THEMES = {"small": "green", "medium": "purple", "hard": "red", "maximum": "green"}


def lum(c):
    return 0.299 * c[0] + 0.587 * c[1] + 0.114 * c[2]


def crop_frame(im, spec):
    cx, y0, y1, w = spec
    c = im.crop((cx - w // 2, y0, cx + w // 2, y1))
    px_w = COLS * 2
    rows = round(px_w * c.height / c.width / 4)
    return c.resize((px_w, rows * 4), Image.LANCZOS), rows


def two_means(pts):
    """pts: (n,3). Делит на светлый и тёмный кластеры, возвращает метки 0/1."""
    ls = pts @ np.array([0.299, 0.587, 0.114])
    c0, c1 = pts[ls.argmax()].copy(), pts[ls.argmin()].copy()
    labels = np.zeros(len(pts), dtype=int)
    for _ in range(4):
        d0 = ((pts - c0) ** 2).sum(axis=1)
        d1 = ((pts - c1) ** 2).sum(axis=1)
        labels = (d1 < d0).astype(int)
        if (labels == 0).any():
            c0 = pts[labels == 0].mean(axis=0)
        if (labels == 1).any():
            c1 = pts[labels == 1].mean(axis=0)
    return labels


def encode_cell(block):
    """block: (4,2,3) float. Возвращает (код брайля, fg|None, bg|None)."""
    pts = block.reshape(8, 3)
    opaque = np.array([lum(p) >= DARK_LUM for p in pts])
    if not opaque.any():
        return 0, None, None
    if opaque.all():
        labels = two_means(pts)
        a, b = pts[labels == 0], pts[labels == 1]
        if len(a) == 0 or len(b) == 0 or np.linalg.norm(a.mean(0) - b.mean(0)) < 48:
            return 0, None, tuple(pts.mean(axis=0))      # сплошная заливка фоном
        code = 0
        for i in range(8):
            if labels[i] == 0:
                code |= BITS[i // 2][i % 2]
        return code, tuple(a.mean(axis=0)), tuple(b.mean(axis=0))
    code = 0
    for i in range(8):
        if opaque[i]:
            code |= BITS[i // 2][i % 2]
    return code, tuple(pts[opaque].mean(axis=0)), None


def hex_of(rgb):
    return "#%02x%02x%02x" % tuple(int(round(v)) for v in rgb)


def build_theme(sheet_name, rainbow):
    im = Image.open(os.path.join(SHEETS, sheet_name + ".png")).convert("RGB")
    cells = {}
    all_colors = []
    for name, spec in {**EMOTIONS, **ACTIONS}.items():
        img, rows = crop_frame(im, spec)
        arr = np.asarray(img).astype(float)
        grid = []
        for cy in range(rows):
            line = []
            for cx in range(COLS):
                blk = arr[cy * 4:(cy + 1) * 4, cx * 2:(cx + 1) * 2]
                code, fg, bg = encode_cell(blk)
                line.append((code, fg, bg))
                for c in (fg, bg):
                    if c is not None:
                        all_colors.append(c)
            grid.append(line)
        cells[name] = (rows, grid)

    # единая палитра темы
    strip = Image.new("RGB", (len(all_colors), 1))
    strip.putdata([tuple(int(round(v)) for v in c) for c in all_colors])
    q = strip.quantize(colors=N_COLORS, method=Image.Quantize.MEDIANCUT,
                       dither=Image.Dither.NONE)
    pal_flat = q.getpalette()[: N_COLORS * 3]
    qidx = list(np.asarray(q)[0])
    lookup = {}
    for c, qi in zip(all_colors, qidx):
        lookup[c] = qi
    used, palette = {}, []

    def pidx(c):
        qi = lookup[c]
        if qi not in used:
            used[qi] = len(palette)
            palette.append(tuple(pal_flat[qi * 3: qi * 3 + 3]))
        return used[qi]

    frames = {}
    for name, (rows, grid) in cells.items():
        ch_rows, fg_rows, bg_rows = [], [], []
        for line in grid:
            chs, fgs, bgs = "", "", ""
            for code, fg, bg in line:
                chs += chr(0x2800 + code) if code else " "
                fgs += CHARS[pidx(fg)] if fg is not None else "."
                bgs += CHARS[pidx(bg)] if bg is not None else "."
            ch_rows.append(chs)
            fg_rows.append(fgs)
            bg_rows.append(bgs)
        frames[name] = {"w": COLS, "h": rows, "ch": ch_rows, "fg": fg_rows, "bg": bg_rows}

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
        "frames": frames,
    }


def preview(theme, data):
    scale = 5
    names = list(data["frames"].keys())
    cols = 5
    fw = COLS * 2 * scale + 12
    fh = max(f["h"] for f in data["frames"].values()) * 4 * scale + 12
    rows_n = (len(names) + cols - 1) // cols
    img = Image.new("RGB", (fw * cols, fh * rows_n), (22, 24, 28))
    px = img.load()
    pal = [tuple(int(c[i:i + 2], 16) for i in (1, 3, 5)) for c in data["palette"]]
    accent = set(data["accent"])

    def tint(idx, cx, cy):
        c = pal[idx]
        if idx in accent:
            _, s, v = colorsys.rgb_to_hsv(*(x / 255 for x in c))
            h = (cx * 0.03 + cy * 0.05) % 1.0
            return tuple(int(x * 255) for x in colorsys.hsv_to_rgb(h, max(s, 0.75), v))
        return c

    for n, name in enumerate(names):
        fr = data["frames"][name]
        ox, oy = (n % cols) * fw + 6, (n // cols) * fh + 6
        for cy in range(fr["h"]):
            for cx in range(fr["w"]):
                ch, fg, bg = fr["ch"][cy][cx], fr["fg"][cy][cx], fr["bg"][cy][cx]
                code = ord(ch) - 0x2800 if ch != " " else 0
                fgc = tint(CHARS.index(fg), cx, cy) if fg != "." else None
                bgc = tint(CHARS.index(bg), cx, cy) if bg != "." else None
                for r in range(4):
                    for c in range(2):
                        on = code & BITS[r][c]
                        col = fgc if on else bgc
                        if col is None:
                            continue
                        x0 = ox + (cx * 2 + c) * scale
                        y0 = oy + (cy * 4 + r) * scale
                        for dy in range(scale):
                            for dx in range(scale):
                                px[x0 + dx, y0 + dy] = col
    os.makedirs(PREVIEW, exist_ok=True)
    img.save(os.path.join(PREVIEW, "preview_%s.png" % theme))


def main():
    os.makedirs(OUT, exist_ok=True)
    for theme, sheet in THEMES.items():
        data = build_theme(sheet, rainbow=(theme == "maximum"))
        with open(os.path.join(OUT, theme + ".json"), "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, separators=(",", ":"))
        preview(theme, data)
        print("%-8s <- %-6s colors=%d accent=%d frames=%d" % (
            theme, sheet, len(data["palette"]), len(data["accent"]), len(data["frames"])))


if __name__ == "__main__":
    sys.exit(main())
