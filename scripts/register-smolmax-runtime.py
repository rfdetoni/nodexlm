#!/usr/bin/env python3
"""Register a locally rebuilt smolmaxxing-patched llama-server binary.

A SHA-256 manifest protects NodexLM from unintentionally launching a different
binary. It does NOT prove that the binary contains correctly ported kernels.
"""
import argparse
import hashlib
import json
from pathlib import Path

FEATURES = {"moe_pager", "weight_streaming"}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--features", nargs="+", required=True, choices=sorted(FEATURES))
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    if not binary.is_file():
        parser.error("--binary must reference a regular file")
    digest = hashlib.sha256()
    with binary.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    manifest = Path(str(binary) + ".smolmax.json")
    if manifest.exists() or manifest.is_symlink():
        parser.error(f"manifest already exists: {manifest}; remove it to explicitly re-register")
    manifest.write_text(json.dumps({"sha256": digest.hexdigest(), "features": sorted(set(args.features))}, indent=2) + "\n")
    manifest.chmod(0o600)
    print(f"Registered {binary} -> {manifest}")
    print("Warning: this checks binary identity, NOT the correctness of its claimed features.")

if __name__ == "__main__":
    main()
