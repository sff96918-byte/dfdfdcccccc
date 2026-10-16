"""Generate build/appicon.ico without any image library.

A 256x256 matrix-green "CC" mark on the app's dark window colour, written as a
PNG-in-ICO (Vista+ format) with a small 64x64 twin. Pure stdlib: zlib for the
PNG payload, struct for the headers, so the build never needs Pillow.

    python build/make_icon.py            # writes build/appicon.ico
"""

from __future__ import annotations

import struct
import zlib
from pathlib import Path

BACKGROUND = (22, 23, 22)  # the window colour the Go build used (#161716)
GLOW = (0, 255, 156)
DIM = (0, 120, 78)

# A 16x16 bitmap font, only what the two glyphs need (C and C).
GLYPH_C = (
    "0011111000",
    "0110001100",
    "1100000000",
    "1100000000",
    "1100000000",
    "1100000000",
    "1100000000",
    "0110001100",
    "0011111000",
    "0000000000",
)


def _png(width: int, height: int, pixels: bytearray) -> bytes:
    raw = bytearray()
    for y in range(height):
        raw.append(0)  # filter: none
        raw += pixels[y * width * 4 : (y + 1) * width * 4]

    def chunk(kind: bytes, payload: bytes) -> bytes:
        return (
            struct.pack(">I", len(payload))
            + kind
            + payload
            + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
        )

    header = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)  # 8-bit RGBA
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", header)
        + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
        + chunk(b"IEND", b"")
    )


def render(size: int) -> bytes:
    pixels = bytearray(size * size * 4)
    scale = max(1, size // 32)
    glyph_w, glyph_h = 10, 10
    text_w = glyph_w * 2 + 2
    left = (size - text_w * scale) // 2
    top = (size - glyph_h * scale) // 2
    for y in range(size):
        for x in range(size):
            index = (y * size + x) * 4
            # a soft radial vignette so the icon does not read as a flat square
            dx = (x - size / 2) / (size / 2)
            dy = (y - size / 2) / (size / 2)
            glow = max(0.0, 1.0 - (dx * dx + dy * dy) ** 0.6) * 0.28
            r = int(BACKGROUND[0] * (1 - glow) + GLOW[0] * glow)
            g = int(BACKGROUND[1] * (1 - glow) + GLOW[1] * glow)
            b = int(BACKGROUND[2] * (1 - glow) + GLOW[2] * glow)
            pixels[index : index + 4] = bytes((r, g, b, 255))
    for row, pattern in enumerate(GLYPH_C):
        for column, cell in enumerate(pattern):
            if cell != "1":
                continue
            for glyph in (0, 1):
                x0 = left + (column + glyph * (glyph_w + 2)) * scale
                y0 = top + row * scale
                for y in range(y0, y0 + scale):
                    for x in range(x0, x0 + scale):
                        if 0 <= x < size and 0 <= y < size:
                            index = (y * size + x) * 4
                            colour = GLOW if glyph == 0 else DIM
                            pixels[index : index + 4] = bytes((colour[0], colour[1], colour[2], 255))
    return _png(size, size, pixels)


def build_ico(path: Path) -> Path:
    images = [render(256), render(64), render(32)]
    sizes = [256, 64, 32]
    header = struct.pack("<HHH", 0, 1, len(images))
    offset = 6 + 16 * len(images)
    entries, blobs = b"", b""
    for size, data in zip(sizes, images):
        entries += struct.pack(
            "<BBBBHHII", size if size < 256 else 0, size if size < 256 else 0, 0, 0, 1, 32, len(data), offset
        )
        blobs += data
        offset += len(data)
    path.write_bytes(header + entries + blobs)
    return path


if __name__ == "__main__":
    target = Path(__file__).resolve().parent / "appicon.ico"
    build_ico(target)
    print("wrote {} ({} bytes)".format(target, target.stat().st_size))
