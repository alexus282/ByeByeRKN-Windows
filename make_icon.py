"""Builds the Windows app's icon from the same art as the Android launcher icon.

Run from anywhere: python make_icon.py
Writes winres/icon.png (embedded into the exe for the window icon) and winres/app.ico.
"""
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent
ART = ROOT / "winres" / "icon-source.webp"
OUT = ROOT / "winres"


def main() -> None:
    art = Image.open(ART).convert("RGBA")
    icon = art.resize((256, 256), Image.LANCZOS)
    icon.save(OUT / "icon.png")
    art.save(
        OUT / "app.ico",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
    )
    for path in (OUT / "icon.png", OUT / "app.ico"):
        print(path, path.stat().st_size)


if __name__ == "__main__":
    main()
