#!/usr/bin/env python3
"""Stage only native executables/modules; reject ambiguous basename collisions."""
import hashlib
import pathlib
import shutil
import sys

EXECUTABLES = {
    "llama-server", "ggml-rpc-server", "rpc-server",
    "llama-server.exe", "ggml-rpc-server.exe", "rpc-server.exe",
}


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def stage(source, target):
    destination = target / "bin"
    destination.mkdir(parents=True, exist_ok=True)
    for path in sorted(source.rglob("*")):
        if not path.is_file():
            continue
        name = path.name
        if not (name in EXECUTABLES or name.endswith((".dll", ".dylib", ".so", ".metallib")) or ".so." in name):
            continue
        # Never stage an alias resolving outside the pinned build directory.
        if not path.resolve().is_relative_to(source.resolve()):
            raise ValueError(f"runtime alias escapes build tree: {path}")
        out = destination / name
        if out.exists():
            if digest(out) != digest(path):
                raise ValueError(f"ambiguous native library: {name}")
        else:
            shutil.copyfile(path, out)
    if not any((destination / name).exists() for name in ("llama-server", "llama-server.exe")):
        raise ValueError("llama-server missing")
    if not any((destination / name).exists() for name in EXECUTABLES if "rpc-server" in name):
        raise ValueError("RPC worker missing")


if __name__ == "__main__":
    stage(pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]))
