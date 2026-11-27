#!/usr/bin/env python3
"""Verify native token/batch/split correlation with actual inference."""
import json
import os
import pathlib
import re
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

server, model = map(lambda s: str(pathlib.Path(s).resolve()), sys.argv[1:])
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
base = f"http://127.0.0.1:{port}"
with tempfile.TemporaryDirectory() as tmp:
    log = pathlib.Path(tmp) / "server.log"
    with log.open("wb") as out:
        proc = subprocess.Popen(
            [server, "-m", model, "--host", "127.0.0.1", "--port", str(port),
             "--parallel", "1", "-c", "256", "-t", "2", "-ngl", "0"],
            env={**os.environ, "NODEXLM_NATIVE_TOKEN_TRACE": "1"},
            stdout=out, stderr=out)
        try:
            deadline = time.monotonic() + 120
            while True:
                if proc.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("native server did not become ready")
                try:
                    with urllib.request.urlopen(base + "/health", timeout=2) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(0.2)
            body = json.dumps({"prompt": "Once upon a time", "n_predict": 8,
                               "temperature": 0, "ignore_eos": True}).encode()
            request = urllib.request.Request(base + "/completion", data=body,
                                              headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(request, timeout=120) as response:
                result = json.load(response)
                assert result["content"], "empty generation"
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            text = log.read_text(errors="replace")
            print(text[-15000:])
    tokens = re.findall(r"NODEXLM_NATIVE_TOKEN batch=(\d+) task=(\d+) slot=(\d+) ordinal=(\d+) attributable=(\d+)", text)
    batches = {batch: (int(inputs), int(status), int(output))
               for batch, inputs, status, output in re.findall(
                   r"NODEXLM_NATIVE_BATCH batch=(\d+) inputs=(\d+) status=(-?\d+) output=(\d+)", text)}
    durations = {batch: int(us) for batch, us in re.findall(
        r"NODEXLM_NATIVE_BATCH batch=(\d+) inputs=\d+ status=-?\d+ output=\d+ decode_us=(\d+)", text)}
    splits = {batch for batch, count in re.findall(
        r"NODEXLM_NATIVE_SPLIT batch=(\d+) split=\d+ nodes=(\d+) backend=[^\n]+", text) if int(count) > 0}
    attributable = [t for t in tokens if t[4] == "1"]
    assert len(attributable) >= 2, tokens
    assert len({(t[1], t[2], t[3]) for t in tokens}) == len(tokens), tokens
    assert all(batches[t[0]] == (1, 0, 1) and t[0] in splits for t in attributable), (tokens, batches, splits)
    assert all(t[0] in durations for t in attributable), durations
    assert any(durations[t[0]] > 0 for t in attributable), durations
    print("Native token/batch/backend graph submission correlation and synchronized decode duration verified")
