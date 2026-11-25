#!/usr/bin/env python3
"""Regression checks for Android PR build selection (no Android SDK needed)."""
import json
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().with_name("native-revision-changed.py")
REV = "internal/llamaruntime/revision.json"


class AndroidScope(unittest.TestCase):
    def test_scope(self):
        cases = [
            ({REV: {"commit": "a", "published": {"linux": "new"}}}, False),
            ({"android/app/src/main/java/Example.kt": "changed"}, False),
            ({"android/app/src/main/res/values/strings.xml": "changed"}, False),
            ({"internal/llamaruntime/patches/rpc-command-batching.patch": "changed"}, False),
            ({REV: {"commit": "b", "published": {"linux": "old"}}}, True),
            ({"android/app/src/main/cpp/native.cpp": "changed"}, True),
            ({"android/rpc-rust/src/lib.rs": "changed"}, True),
            ({"android/build.gradle.kts": "changed"}, True),
            ({"android/app/build.gradle.kts": "changed"}, True),
            ({"internal/llamaruntime/patches/unix-rpc.patch": "changed"}, True),
            ({"scripts/native-revision-changed.py": "changed"}, True),
            ({".github/workflows/android-apk.yml": "changed"}, True),
            ({REV: {"commit": "a", "published": {"linux": "new"}},
              "android/app/src/main/java/Example.kt": "changed"}, False),
        ]
        for changes, expected in cases:
            with self.subTest(changes=changes), tempfile.TemporaryDirectory() as tmp:
                root = pathlib.Path(tmp)

                def git(*args):
                    subprocess.run(["git", *args], cwd=root, check=True,
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

                def write(name, value):
                    file = root / name
                    file.parent.mkdir(parents=True, exist_ok=True)
                    file.write_text(json.dumps(value) if isinstance(value, dict) else value,
                                    encoding="utf-8")

                git("init", "-q")
                git("config", "user.email", "ci@example.invalid")
                git("config", "user.name", "CI test")
                write(REV, {"commit": "a", "published": {"linux": "old"}})
                git("add", ".")
                git("commit", "-qm", "base")
                for name, value in changes.items():
                    write(name, value)
                git("add", ".")
                git("commit", "-qm", "candidate")
                actual = subprocess.check_output(
                    ["python3", str(SCRIPT), "--android-pr"], cwd=root, text=True
                ).strip()
                self.assertEqual(actual, str(expected).lower())


if __name__ == "__main__":
    unittest.main()
