#!/usr/bin/env python3
"""One-build smoke test for the complete Smolmax CPU port.

Compare real greedy completions against a pinned stock runtime. An optional
large dense model exercises real streaming and checks that stream zones are
nonzero, not just that a mode flag was accepted.
"""
import argparse
import hashlib
import json
import os
import pathlib
import re
import socket
import subprocess
import tempfile
import time
import urllib.request


def checked_model(path, expected):
    path = path.resolve(strict=True)
    digest = hashlib.sha256()
    with path.open("rb") as model:
        for chunk in iter(lambda: model.read(1024 * 1024), b""):
            digest.update(chunk)
    if digest.hexdigest() != expected:
        raise RuntimeError("model checksum mismatch")
    return path


def stream_evidence(log):
    """Check the entire log, retaining bounded evidence and no model data."""
    zones = registrations = traces = 0
    errors = False
    for line in log:
        line = line[:65536]
        match = re.search(r"([0-9.]+) MB in stream zones", line)
        if match and float(match[1]) > 0:
            zones += 1
        match = re.search(r"ggml_cpu_stream_register: stream zone ([0-9.]+) MB", line)
        if match and float(match[1]) > 0:
            registrations += 1
        match = re.search(r"trace of (\d+) zone tensors recorded", line)
        if match and int(match[1]) > 1:
            traces += 1
        if "stream VERIFY" in line or "cannot stream" in line:
            errors = True
    return zones, registrations, traces, errors


def generate(server, model, profile=None, no_repack=False):
    # Ignore inherited experimental switches: validate a reproducible profile.
    env = {k: v for k, v in os.environ.items() if not k.startswith(("GGML_", "LLAMA_", "NODEXLM_SMOLMAX_"))}
    env.update(profile or {})
    env["LD_LIBRARY_PATH"] = str(server.parent) + os.pathsep + env.get("LD_LIBRARY_PATH", "")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with tempfile.TemporaryDirectory() as tmp:
        log = pathlib.Path(tmp) / "server.log"
        with log.open("wb") as out:
            proc = subprocess.Popen(
                [str(server), "-m", str(model), "--host", "127.0.0.1", "--port", str(port),
                 "--parallel", "1", "-c", "256", "-t", "2", "-ngl", "0", "-lv", "4" if profile else "3"] + (["--no-repack"] if no_repack else []),
                env=env, stdout=out, stderr=out)
            try:
                deadline = time.monotonic() + 180
                while True:
                    if proc.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("native server did not become ready")
                    try:
                        with opener.open(base + "/health", timeout=2) as response:
                            if response.status == 200:
                                break
                    except OSError:
                        time.sleep(.25)
                body = json.dumps({"prompt": "Once upon a time", "n_predict": 8,
                                   "temperature": 0, "seed": 42,
                                   "ignore_eos": True, "cache_prompt": False}).encode()
                request = urllib.request.Request(base + "/completion", data=body,
                                                  headers={"Content-Type": "application/json"})
                with opener.open(request, timeout=180) as response:
                    raw = response.read(1024 * 1024 + 1)
                if len(raw) > 1024 * 1024:
                    raise RuntimeError("response exceeded limit")
                result = json.loads(raw)
                if not result.get("content") or result.get("tokens_predicted", 0) != 8:
                    raise RuntimeError("incomplete native generation")
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
                with log.open("rb") as source:
                    source.seek(max(0, log.stat().st_size - 64 * 1024))
                    diagnostic = source.read().decode(errors="replace")
                print(diagnostic[-8000:])
                with log.open("r", encoding="utf-8", errors="replace") as source:
                    evidence = stream_evidence(source)
    return result["content"], evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--baseline", required=True, type=pathlib.Path)
    parser.add_argument("--model", required=True, type=pathlib.Path)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--stream-model", type=pathlib.Path)
    parser.add_argument("--stream-sha256")
    args = parser.parse_args()
    model = checked_model(args.model, args.sha256)
    candidate = args.candidate.resolve(strict=True)
    baseline = args.baseline.resolve(strict=True)
    expected, _ = generate(baseline, model)
    actual, _ = generate(candidate, model)
    if expected != actual:
        raise RuntimeError("greedy tiny-model output differs from stock baseline")
    print("Stock/candidate greedy CPU smoke passed")
    if args.stream_model:
        if not args.stream_sha256:
            parser.error("--stream-sha256 required")
        dense = checked_model(args.stream_model, args.stream_sha256)
        normal, _ = generate(candidate, dense, no_repack=True)
        streamed, evidence = generate(candidate, dense, {
            "LLAMA_STREAM_RESIDENT_MB": "64",
            "GGML_STREAM_WINDOW_MB": "32",
            "GGML_STREAM_READERS": "2",
            "GGML_STREAM_VERIFY": "1",
            "GGML_STREAM_STATS": "1",
        }, no_repack=True)
        zones, registrations, traces, errors = evidence
        if not (zones and registrations and traces):
            raise RuntimeError(f"missing streaming evidence: zones={zones}, registrations={registrations}, traces={traces}")
        if errors or streamed != normal:
            raise RuntimeError("streamed output or tensor bytes differ")
        print("Dense streaming smoke passed with nonzero zones")


if __name__ == "__main__":
    main()
