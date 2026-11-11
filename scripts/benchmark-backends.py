#!/usr/bin/env python3
"""Compare two local GGUF engines with the same prompt, hardware and model.

Python standard library only; no web downloads, sudo, shell evaluation or
model modifications. The mistral.rs server is unauthenticated: use loopback.
"""
from __future__ import annotations

import argparse
import json
import os
import pathlib
import signal
import socket
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request

PROMPT = "Explain in two short paragraphs how a small local AI cluster works."


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
        server.bind(("127.0.0.1", 0))
        return int(server.getsockname()[1])


def command(engine: str, binary: str, model: pathlib.Path, port: int,
            context: int, threads: int, cpu: bool) -> list[str]:
    if engine == "llama.cpp":
        cmd = [binary, "--host", "127.0.0.1", "--port", str(port),
               "--model", str(model), "--alias", model.name,
               "--ctx-size", str(context), "--threads", str(threads),
               "--gpu-layers", "0" if cpu else "auto", "--no-webui"]
    elif engine == "mistralrs":
        cmd = [binary, "serve", "--host", "127.0.0.1", "--port", str(port),
               "--no-ui", "--max-model-len", str(context), "-f", str(model)]
        if cpu:
            cmd.append("--cpu")
    else:
        raise ValueError(f"unsupported engine: {engine}")
    return cmd


def http_json(url: str, payload=None, timeout: float = 5.0):
    raw = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url, data=raw, headers={
        "Content-Type": "application/json",
    }, method="POST" if raw is not None else "GET")
    with urllib.request.urlopen(req, timeout=timeout) as response:
        return json.load(response)


def ready(engine: str, base: str) -> bool:
    try:
        if engine == "llama.cpp":
            return http_json(base + "/health", timeout=1).get("status") in (
                "ok", "no slot available"
            )
        data = http_json(base + "/v1/models", timeout=1)
        return any(x.get("status") == "loaded" for x in data.get("data", []))
    except (OSError, ValueError, urllib.error.URLError):
        return False


def rss_bytes(pid: int) -> int | None:
    # Linux primary process RSS; do not label this as cluster-wide peak memory.
    try:
        lines = pathlib.Path(f"/proc/{pid}/status").read_text().splitlines()
        for line in lines:
            if line.startswith("VmRSS:"):
                return int(line.split()[1]) * 1024
    except (OSError, ValueError, IndexError):
        pass
    return None


def benchmark_one(engine: str, binary: str, model: pathlib.Path, opts):
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    cmd = command(engine, binary, model, port, opts.context, opts.threads, opts.cpu)
    start = time.monotonic()
    # Use a new subprocess for each engine to avoid hidden KV/cache reuse.
    with open(os.devnull, "wb") as sink:
        proc = subprocess.Popen(cmd, stdin=subprocess.DEVNULL, stdout=sink,
                                stderr=sink, shell=False)
        load_s = None
        peak_rss = None
        results = []
        try:
            while time.monotonic() - start < opts.startup_timeout:
                if proc.poll() is not None:
                    raise RuntimeError(f"{engine} exited during startup ({proc.returncode})")
                sample = rss_bytes(proc.pid)
                if sample is not None:
                    peak_rss = max(peak_rss or 0, sample)
                if ready(engine, base):
                    load_s = time.monotonic() - start
                    break
                time.sleep(.25)
            if load_s is None:
                raise TimeoutError(f"{engine} did not load within {opts.startup_timeout}s")

            # One warm-up request and N measured requests. Deterministic decode
            # settings; actual token counts come from the server, not estimates.
            prompt = opts.prompt
            payload = {
                "model": model.name if engine == "llama.cpp" else "default",
                "messages": [{"role": "user", "content": prompt}],
                "temperature": 0, "max_tokens": opts.max_tokens, "stream": False,
            }
            for i in range(opts.iterations + 1):
                began = time.monotonic()
                output = http_json(base + "/v1/chat/completions", payload, timeout=opts.request_timeout)
                elapsed = time.monotonic() - began
                usage = output.get("usage") or {}
                tokens = usage.get("completion_tokens")
                if not isinstance(tokens, int) or tokens < 0:
                    raise ValueError(f"{engine} did not report valid completion_tokens")
                peak = rss_bytes(proc.pid)
                if peak is not None:
                    peak_rss = max(peak_rss or 0, peak)
                if i > 0:
                    results.append({"seconds": round(elapsed, 4), "tokens": tokens,
                                    "end_to_end_tokens_per_second":
                                        round(tokens / elapsed, 3) if elapsed > 0 else None})
            med = statistics.median(row["end_to_end_tokens_per_second"] for row in results)
            return {
                "backend": engine,
                "model": model.name,
                "cpu_only": opts.cpu,
                "load_seconds": round(load_s, 3),
                "median_end_to_end_tokens_per_second": round(med, 3),
                "peak_main_process_rss_bytes": peak_rss,
                "samples": results,
            }
        finally:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--model", required=True, type=pathlib.Path)
    p.add_argument("--llama-bin", required=True, type=pathlib.Path)
    p.add_argument("--mistralrs-bin", required=True, type=pathlib.Path)
    p.add_argument("--cpu", action="store_true", help="force CPU-only on both engines")
    p.add_argument("--threads", type=int, default=4, help="llama.cpp threads; mistral.rs uses internal scheduling")
    p.add_argument("--context", type=int, default=2048)
    p.add_argument("--iterations", type=int, default=3)
    p.add_argument("--max-tokens", type=int, default=64)
    p.add_argument("--startup-timeout", type=int, default=300)
    p.add_argument("--request-timeout", type=int, default=120)
    p.add_argument("--prompt", default=PROMPT)
    p.add_argument("--output", type=pathlib.Path)
    p.add_argument("--dry-run", action="store_true", help="validate commands only; do not start engines")
    opts = p.parse_args()
    if opts.context < 256 or opts.iterations < 1 or opts.iterations > 30 or opts.max_tokens < 1 or opts.max_tokens > 2048 or opts.threads < 1:
        p.error("invalid benchmark limits")
    model = opts.model.expanduser().resolve()
    if not opts.dry_run and (not model.is_file() or model.suffix.lower() != ".gguf"):
        p.error("--model must be an existing GGUF file")
    engines = {"llama.cpp": opts.llama_bin, "mistralrs": opts.mistralrs_bin}
    for engine, path in engines.items():
        binary = str(path.expanduser().resolve())
        if not opts.dry_run and not (os.path.isfile(binary) and os.access(binary, os.X_OK)):
            p.error(f"{engine} binary must be an existing executable")
        engines[engine] = binary
    if opts.dry_run:
        for engine, binary in engines.items():
            print(json.dumps({"engine": engine, "args":
                command(engine, binary, model, 1234, opts.context, opts.threads, opts.cpu)}))
        return 0
    report = {
        "measured_at_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "method": "warmup=1, sequential requests, wall-clock throughput including prefill",
        "warning": "Do not compare aggregate RAM/VRAM or distributed RPC from this single-host benchmark",
        "results": [],
    }
    try:
        for engine, binary in engines.items():
            report["results"].append(benchmark_one(engine, binary, model, opts))
    except (OSError, TimeoutError, RuntimeError, ValueError, urllib.error.URLError) as error:
        print(f"benchmark failed: {error}", file=sys.stderr)
        return 1
    output = json.dumps(report, indent=2)
    print(output)
    if opts.output:
        target = opts.output.expanduser().resolve()
        target.write_text(output + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
