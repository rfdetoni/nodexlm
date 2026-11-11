#!/usr/bin/env python3
"""Print true if pinned llama.cpp code/patch changed, ignoring published SHA pins.

CI can update published release checksums without rebuilding the same native
revision across five runners or recompiling the Android APK.
"""
import json
import pathlib
import subprocess

REVISION = "internal/llamaruntime/revision.json"


def main():
    current = json.loads(pathlib.Path(REVISION).read_text(encoding="utf-8"))
    try:
        old = json.loads(subprocess.check_output(["git", "show", "HEAD^:" + REVISION], text=True))
        changed = subprocess.check_output(
            ["git", "diff", "--name-only", "HEAD^", "HEAD", "--", "internal/llamaruntime/patches"],
            text=True,
        ).strip()
    except subprocess.CalledProcessError:
        # A shallow history or newly added manifest is not proof of sameness.
        print("true")
        return
    identity = ("version", "commit", "unix_rpc")
    print("true" if changed or any(old.get(k) != current.get(k) for k in identity) else "false")


if __name__ == "__main__":
    main()
