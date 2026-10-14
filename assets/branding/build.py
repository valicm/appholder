import os, shutil, cairosvg

ROOT = os.path.dirname(os.path.abspath(__file__))
SVG = os.path.join(ROOT, "svg")
OUT = os.path.join(ROOT, "icons", "hicolor")
DESKTOP_OUT = os.path.abspath(os.path.join(ROOT, "..", "..", "internal", "desktop", "assets", "hicolor"))
NAME = "appholder"

shutil.rmtree(os.path.join(ROOT, "icons"), ignore_errors=True)

def put(src, rel):
    dst = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copy(os.path.join(SVG, src), dst)

put("appholder-app-icon.svg", f"scalable/apps/{NAME}.svg")
put("appholder-symbolic.svg", f"symbolic/apps/{NAME}-symbolic.svg")

for size in (16, 24, 32, 48, 64, 128, 256):
    src = "appholder-app-icon-small.svg" if size <= 24 else "appholder-app-icon.svg"
    dst = os.path.join(OUT, f"{size}x{size}", "apps", f"{NAME}.png")
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    cairosvg.svg2png(url=os.path.join(SVG, src), write_to=dst, output_width=size, output_height=size)

art = os.path.join(ROOT, "artwork")
os.makedirs(art, exist_ok=True)
cairosvg.svg2png(url=os.path.join(SVG, "appholder-app-icon.svg"), write_to=os.path.join(art, f"{NAME}-512.png"), output_width=512, output_height=512)
cairosvg.svg2png(url=os.path.join(SVG, "appholder.svg"), write_to=os.path.join(art, f"{NAME}-mark-512.png"), output_width=512, output_height=512)
cairosvg.svg2png(url=os.path.join(SVG, "appholder-on-dark.svg"), write_to=os.path.join(art, f"{NAME}-mark-on-dark-512.png"), output_width=512, output_height=512)
shutil.rmtree(DESKTOP_OUT, ignore_errors=True)
shutil.copytree(OUT, DESKTOP_OUT)
print("done")
