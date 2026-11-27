#!/usr/bin/env python3
"""Offline regression checks for the authenticated physical-cluster benchmark."""
import json
import os
import pathlib
import runpy
import stat
import tempfile
import unittest
import urllib.request

MODULE = runpy.run_path(str(pathlib.Path(__file__).with_name("benchmark-cluster.py")))
report = MODULE["report"]
endpoint = MODULE["endpoint"]
traffic_delta = MODULE["traffic_delta"]
save = MODULE["save"]


def sample(rate, topo="", fingerprint="a" * 64, model="tiny.gguf"):
    return {
        "kind": "benchmark",
        "model": model,
        "fingerprint": fingerprint,
        "benchmark_topology": topo,
        "rate_basis": "native_decode",
        "tokens_per_second": rate,
        "tokens": 16,
    }


def series(rates=(12, 16, 14), changed=False):
    node = "b" * 64
    rows = []
    for i, rate in enumerate(rates):
        before = {node: {"relay_epoch": 1, "bytes_to_guest": 100, "bytes_from_guest": 20,
                         "writes_to_guest": 1, "writes_from_guest": 2}}
        after = {node: {"relay_epoch": 1, "bytes_to_guest": 300, "bytes_from_guest": 60,
                        "writes_to_guest": 3, "writes_from_guest": 5}}
        rows.append({
            "inference": sample(rate, node + ":0:RPC0:1" if not (changed and i == 1)
                                else node + ":1:RPC0:1", model="tiny.gguf@bench-cluster"),
            "rpc_stats_before": before, "rpc_stats_after": after,
        })
    return rows


class BenchmarkAcceptance(unittest.TestCase):
    def test_compares_same_native_runtime_and_topology(self):
        result = report("tiny.gguf", sample(10), series(), {})
        self.assertEqual(result["median_cluster_tokens_per_second"], 14)
        self.assertEqual(result["cluster_to_host_ratio"], 1.4)
        self.assertEqual(result["rpc_relay_deltas"][0]["b" * 64]["bytes_to_guest"], 200)

    def test_cluster_only_does_not_invent_host_baseline(self):
        result = report("tiny.gguf", None, series(), {})
        self.assertIsNone(result["host_tokens_per_second"])
        self.assertIsNone(result["cluster_to_host_ratio"])

    def test_rejects_change_in_topology_or_fingerprint(self):
        with self.assertRaisesRegex(ValueError, "topology"):
            report("tiny.gguf", sample(10), series(changed=True), {})
        with self.assertRaisesRegex(ValueError, "fingerprint"):
            report("tiny.gguf", sample(10, fingerprint="c" * 64), series(), {})
        with self.assertRaisesRegex(ValueError, "basis"):
            host = sample(10)
            host["rate_basis"] = "end_to_end"
            report("tiny.gguf", host, series(), {})

    def test_rejects_unknown_or_invalid_throughput(self):
        for value in (float("nan"), float("inf"), 0, -3, None, "fast"):
            rows = series()
            rows[0]["inference"]["tokens_per_second"] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                report("tiny.gguf", sample(10), rows, {})

    def test_rejects_reset_native_transfer_counter(self):
        rows = series()
        rows[0]["rpc_stats_after"]["b" * 64]["bytes_to_guest"] = 50
        with self.assertRaisesRegex(ValueError, "changed/reset"):
            traffic_delta(rows[0])

    def test_rejects_recreated_or_missing_relay_even_with_increasing_counters(self):
        for epoch in (None, 0, 2, True):
            row = series()[0]
            row["rpc_stats_after"]["b" * 64]["relay_epoch"] = epoch
            with self.subTest(epoch=epoch), self.assertRaisesRegex(ValueError, "epoch"):
                traffic_delta(row)
        row = series()[0]
        row["rpc_stats_after"].clear()
        with self.assertRaisesRegex(ValueError, "membership"):
            traffic_delta(row)

    def test_http_requires_numeric_loopback_and_no_credentials(self):
        self.assertEqual(endpoint("http://127.0.0.1:8787"), "http://127.0.0.1:8787")
        for unsafe in ("http://localhost:8787", "http://192.168.1.10:8787",
                       "https://user:pass@example.com", "https://example.com/other",
                       "https://example.com?token=secret"):
            with self.subTest(url=unsafe), self.assertRaises(ValueError):
                endpoint(unsafe)

    def test_redirect_never_forwards_bearer(self):
        redirect = MODULE["NoRedirect"]()
        request = urllib.request.Request("https://source.example", headers={"Authorization": "Bearer secret"})
        self.assertIsNone(redirect.redirect_request(
            request, None, 302, "Found", {}, "https://untrusted.example"
        ))

    def test_report_written_atomically_private_without_token(self):
        with tempfile.TemporaryDirectory() as dirname:
            out = pathlib.Path(dirname) / "measurements.json"
            save(out, report("tiny.gguf", sample(10), series(), {}))
            self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
            self.assertEqual(json.loads(out.read_text())["median_cluster_tokens_per_second"], 14)


if __name__ == "__main__":
    unittest.main()
