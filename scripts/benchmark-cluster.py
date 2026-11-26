#!/usr/bin/env python3
"""Controlled Host vs distributed NodexLM inference benchmark.

Requires an unloaded model, a live enrolled cluster and an administrator token
in an environment variable. Never modifies settings, installs runtimes or
downloads models. Server timing bases and topology must remain comparable.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import pathlib
import re
import ssl
import statistics
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None  # Never forward the administrator bearer token.


def endpoint(value: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or (
        parsed.username or parsed.password or parsed.query or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        raise ValueError("expected a Host HTTP(S) origin without credentials or path")
    if parsed.scheme == "http" and parsed.hostname not in ("127.0.0.1", "::1"):
        raise ValueError("unencrypted Host API allowed only on numeric loopback")
    return value.rstrip("/")


def client(ca: pathlib.Path | None):
    context = ssl.create_default_context(cafile=str(ca) if ca else None)
    return urllib.request.build_opener(
        urllib.request.HTTPSHandler(context=context), NoRedirect()
    )


def post(opener, base: str, path: str, token: str, data: dict, timeout: int):
    body = json.dumps(data, separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        base + path, data=body, method="POST",
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    try:
        with opener.open(request, timeout=timeout) as response:
            if response.status != 200:
                raise ValueError(f"{path}: unexpected HTTP {response.status}")
            raw = response.read(5 * 1024 * 1024 + 1)
            if len(raw) > 5 * 1024 * 1024:
                raise ValueError("benchmark response exceeds 5 MiB")
            return json.loads(raw)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{path}: Host returned HTTP {error.code}") from None


def valid_sample(data, name: str):
    if not isinstance(data, dict) or data.get("kind") != "benchmark":
        raise ValueError(f"{name}: not a controlled benchmark")
    rate = data.get("tokens_per_second")
    if not isinstance(rate, (float, int)) or isinstance(rate, bool) or (
        not math.isfinite(rate) or rate <= 0
    ):
        raise ValueError(f"{name}: invalid measured tokens/s")
    fingerprint = data.get("fingerprint", "")
    if not isinstance(fingerprint, str) or not re.fullmatch(r"[0-9a-f]{64}", fingerprint):
        raise ValueError(f"{name}: missing model/runtime fingerprint")
    if data.get("rate_basis") not in ("native_decode", "end_to_end"):
        raise ValueError(f"{name}: unknown token-rate basis")
    if data.get("suspect") is True:
        raise ValueError(f"{name}: benchmark was marked suspect")
    if not isinstance(data.get("tokens"), int) or data["tokens"] <= 0:
        raise ValueError(f"{name}: missing runtime token counts")
    return data


COUNTERS = ("bytes_to_guest", "bytes_from_guest", "writes_to_guest", "writes_from_guest")


def traffic_delta(round_data):
    before = round_data.get("rpc_stats_before")
    after = round_data.get("rpc_stats_after")
    if not isinstance(before, dict) or not isinstance(after, dict):
        raise ValueError("missing paired native relay snapshots")
    results = {}
    for node, current in after.items():
        if node not in before:
            continue
        previous = before[node]
        if not isinstance(previous, dict) or not isinstance(current, dict):
            raise ValueError("malformed relay stats")
        counters = {}
        for key in COUNTERS:
            first, last = previous.get(key), current.get(key)
            if (not isinstance(first, int) or isinstance(first, bool) or
                not isinstance(last, int) or isinstance(last, bool) or
                first < 0 or last < first):
                raise ValueError("RPC counter changed/reset during benchmark")
            counters[key] = last - first
        if any(counters.values()):
            results[node] = counters
    return results


def report(model: str, host: dict | None, rounds: list, guests: dict):
    if not isinstance(rounds, list) or not 2 <= len(rounds) <= 10:
        raise ValueError("cluster series must contain 2–10 rounds")
    samples = []
    traffic = []
    for i, row in enumerate(rounds):
        if not isinstance(row, dict):
            raise ValueError("malformed cluster round")
        sample = valid_sample(row.get("inference"), f"cluster round {i + 1}")
        if sample.get("model") != model + "@bench-cluster":
            raise ValueError("cluster result belongs to a different model")
        if not isinstance(sample.get("benchmark_topology"), str) or not sample["benchmark_topology"]:
            raise ValueError("cluster round is missing native placement")
        samples.append(sample)
        traffic.append(traffic_delta(row))
    basis = samples[0]["rate_basis"]
    fingerprint = samples[0]["fingerprint"]
    topology = samples[0]["benchmark_topology"]
    for sample in samples[1:]:
        if sample["rate_basis"] != basis or sample["fingerprint"] != fingerprint or (
            sample["benchmark_topology"] != topology
        ):
            raise ValueError("cluster topology, runtime or timing basis changed")
    if host is not None:
        host = valid_sample(host, "Host")
        if host.get("model") != model:
            raise ValueError("Host result belongs to a different model")
        if host["fingerprint"] != fingerprint:
            raise ValueError("Host and cluster fingerprint differ")
        if host["rate_basis"] != basis:
            raise ValueError("Host and cluster timing basis differ")
    for node, sample in guests.items():
        sample = valid_sample(sample, f"Guest {node}")
        if sample.get("model") != model + "@" + node:
            raise ValueError("Guest result belongs to a different model")
        if sample["fingerprint"] != fingerprint:
            raise ValueError(f"Guest {node} fingerprint differs")
        if sample["rate_basis"] != basis:
            raise ValueError(f"Guest {node} timing basis differs")
    cluster_rates = [s["tokens_per_second"] for s in samples]
    median_cluster = statistics.median(cluster_rates)
    return {
        "schema": "nodexlm-cluster-ab-v1",
        "measured_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "model": model,
        "fingerprint": fingerprint,
        "rate_basis": basis,
        "cluster_topology": topology,
        "host": host,
        "guests": guests,
        "cluster_rounds": samples,
        "rpc_relay_deltas": traffic,
        "median_cluster_tokens_per_second": median_cluster,
        "host_tokens_per_second": host["tokens_per_second"] if host else None,
        "cluster_to_host_ratio": median_cluster / host["tokens_per_second"] if host else None,
        "note": "Native decode tokens/s when available; relay bytes are not GPU/CPU kernel utilization.",
    }


def save(output: pathlib.Path, body: dict):
    # Atomic, private output. Never store an admin token in the result.
    root = output.parent.resolve(strict=True)
    if output.is_dir():
        raise ValueError("output is a directory")
    with tempfile.NamedTemporaryFile(
        mode="w", encoding="utf-8", dir=root, prefix=".nodexlm-bench-", delete=False
    ) as target:
        tmp = pathlib.Path(target.name)
        try:
            os.chmod(tmp, 0o600)
            json.dump(body, target, indent=2, ensure_ascii=False, allow_nan=False)
            target.write("\n")
            target.flush()
            os.fsync(target.fileno())
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise
    os.replace(tmp, output)


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--url", default="http://127.0.0.1:8787")
    p.add_argument("--model", required=True, help="GGUF filename already on the Host")
    p.add_argument("--rounds", type=int, default=3, choices=range(2, 11), metavar="[2-10]")
    p.add_argument("--token-env", default="NODEXLM_ADMIN_TOKEN")
    p.add_argument("--ca-cert", type=pathlib.Path, help="trust the enrolled Host's PEM certificate")
    p.add_argument("--guest", action="append", default=[], help="benchmark a specific enrolled Guest ID")
    p.add_argument("--cluster-only", action="store_true", help="Host cannot fit the full model")
    p.add_argument("--output", type=pathlib.Path, required=True)
    args = p.parse_args(argv)
    if not re.fullmatch(r"[A-Za-z0-9_.-]{1,180}[.]gguf", args.model, re.I):
        p.error("--model must be a GGUF filename without directories")
    if not re.fullmatch(r"[A-Z][A-Z0-9_]{0,79}", args.token_env):
        p.error("invalid token environment variable name")
    for guest in args.guest:
        if not re.fullmatch(r"[a-f0-9]{64}", guest):
            p.error("--guest requires a 64-character authenticated node ID")
    key = os.environ.get(args.token_env, "")
    if not key:
        p.error(f"missing {args.token_env} environment variable")
    base = endpoint(args.url)
    opener = client(args.ca_cert)
    timeout = 30 * 60
    host = None if args.cluster_only else post(
        opener, base, "/api/bench/inference", key, {"model": args.model, "node_id": ""}, timeout
    )
    guests = {}
    for guest in args.guest:
        guests[guest] = post(
            opener, base, "/api/bench/inference", key,
            {"model": args.model, "node_id": guest}, timeout
        )
    rounds = post(
        opener, base, "/api/bench/inference-series", key,
        {"model": args.model, "rounds": args.rounds}, timeout
    )
    result = report(args.model, host, rounds, guests)
    save(args.output, result)
    print(f"Cluster: {result['median_cluster_tokens_per_second']:.2f} t/s")
    if host is not None:
        print(f"Host: {result['host_tokens_per_second']:.2f} t/s · "
              f"Cluster/Host: {result['cluster_to_host_ratio']:.3f}x")
    print(f"Report: {args.output}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as exc:
        print(f"Benchmark rejected: {exc}", file=sys.stderr)
        sys.exit(1)
