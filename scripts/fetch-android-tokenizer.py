#!/usr/bin/env python3
"""Retrieve reproducible 16 KiB-compatible DJL tokenizer JNI for Android.

DJL's last Maven Central Android tokenizer AAR (0.33.0) contains an arm64
binary aligned for 4 KiB pages. DJL publishes its newer 0.40.0 build on its
official binary CDN, with arm64 ELF segments aligned to 16 KiB. We pin both
ABIs by SHA-256 instead of shipping a mutable SNAPSHOT or relaxing Android
page-size verification. No model weights are downloaded by this script.
"""
from __future__ import annotations

import hashlib
import os
import pathlib
import sys
import tempfile
import urllib.request

UPSTREAM = "https://publish.djl.ai/tokenizers/0.21.0/jnilib/0.40.0/android"
HASHES = {
    "armeabi-v7a": "0499514c145bd266783260087968b7c5422f1c7f2258226c8a5ac454868728ec",
    "arm64-v8a": "b7f08219d3369d0d3b78625938af591d91f475d7a17e01fe99497138432999c4",
}
MAX_BYTES = 64 * 1024 * 1024


def digest(path: pathlib.Path) -> str:
    sha = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(256 * 1024), b""):
            sha.update(chunk)
    return sha.hexdigest()


def install(root: pathlib.Path, arch: str, expected: str) -> None:
    target_dir = root / arch
    target_dir.mkdir(parents=True, exist_ok=True)
    target = target_dir / "libdjl_tokenizer.so"
    if target.exists() and digest(target) == expected:
        print(f"Verified cached DJL tokenizer: {arch}")
        return
    fd, temp = tempfile.mkstemp(prefix=".libdjl-", suffix=".so", dir=target_dir)
    os.close(fd)
    staged = pathlib.Path(temp)
    try:
        url = f"{UPSTREAM}/{arch}/libdjl_tokenizer.so"
        request = urllib.request.Request(url, headers={"User-Agent": "NodexLM-Android/1"})
        with urllib.request.urlopen(request, timeout=50) as source, staged.open("wb") as output:
            length = 0
            while True:
                chunk = source.read(256 * 1024)
                if not chunk:
                    break
                length += len(chunk)
                if length > MAX_BYTES:
                    raise ValueError(f"Tokenizer binary over size limit for {arch}")
                output.write(chunk)
            output.flush()
            os.fsync(output.fileno())
        found = digest(staged)
        if found != expected:
            raise ValueError(f"Upstream DJL tokenizer checksum mismatch for {arch}: {found}")
        if staged.read_bytes()[:4] != b"\x7fELF":
            raise ValueError(f"DJL tokenizer {arch} is not a native ELF")
        os.replace(staged, target)
        print(f"Verified DJL tokenizer {arch}: {found}")
    finally:
        staged.unlink(missing_ok=True)


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("Usage: fetch-android-tokenizer.py <android-jniLibs-dir>")
    root = pathlib.Path(sys.argv[1]).resolve()
    for arch, expected in HASHES.items():
        install(root, arch, expected)


if __name__ == "__main__":
    main()
