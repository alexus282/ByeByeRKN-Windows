"""Builds the Windows app's icon from the same art as the Android launcher icon.

Run from the project root: python windows/make_icon.py
Writes windows/winres/icon.png (embedded into the exe for the window icon) and app.ico.
"""
from pathlib import Path

from PIL import Image

OUT = Path("windows/winres")


def main() -> None:
    art = Image.open("art/icon-source.webp").convert("RGBA")
    OUT.mkdir(parents=True, exist_ok=True)
    icon = art.resize((256, 256), Image.LANCZOS)
    icon.save(OUT / "icon.png")
    art.save(OUT / "app.ico", sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
    for path in (OUT / "icon.png", OUT / "app.ico"):
        print(path, path.stat().st_size)


if __name__ == "__main__":
    main()
