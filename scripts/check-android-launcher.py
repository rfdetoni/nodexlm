#!/usr/bin/env python3
"""Validate launcher manifest refs, layers and five-node icon geometry."""
from pathlib import Path
from xml.etree import ElementTree as ET

res = Path(__file__).resolve().parents[1] / "android/app/src/main/res"
ns = "{http://schemas.android.com/apk/res/android}"
app = ET.parse(res.parent / "AndroidManifest.xml").getroot().find("application")
assert app is not None
assert app.get(ns + "icon") == "@mipmap/ic_launcher"
assert app.get(ns + "roundIcon") == "@mipmap/ic_launcher_round"
layers = {
    "background": "@color/nodexlm_launcher_background",
    "foreground": "@drawable/ic_nodexlm_launcher_foreground",
    "monochrome": "@drawable/ic_nodexlm_launcher_monochrome",
}
for name in ["ic_launcher", "ic_launcher_round"]:
    root = ET.parse(res / "mipmap-anydpi-v26" / (name + ".xml")).getroot()
    assert root.tag == "adaptive-icon"
    for tag, drawable in layers.items():
        item = root.find(tag)
        assert item is not None and item.get(ns + "drawable") == drawable

colors = ET.parse(res / "values/colors.xml").getroot()
background = colors.find("./color[@name='nodexlm_launcher_background']")
assert background is not None and (background.text or "").strip().lower() == "#202327"

path = "M36,33 L36,75 M36,33 L54,54 L72,75 M72,33 L72,75"
for name in ["ic_nodexlm_launcher_foreground", "ic_nodexlm_launcher_monochrome"]:
    root = ET.parse(res / "drawable" / (name + ".xml")).getroot()
    assert root.tag == "vector"
    assert root.get(ns + "viewportWidth") == "108"
    assert root.get(ns + "viewportHeight") == "108"
    assert any(p.get(ns + "pathData") == path for p in root.findall("path"))
    assert len(root.findall("path")) >= 2
print("Adaptive, round, monochrome and five-node launcher resources: PASS")
