#!/usr/bin/env python3
"""Render reviewed public dashboard exports without a backend or dependencies."""
import argparse
import html
import json
import math
import pathlib
import re


def render(root):
    lines = []
    for path in sorted(root.glob("*.json")):
        if path.stat().st_size > 2 * 1024 * 1024:
            raise ValueError(f"oversize report: {path}")
        reports = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(reports, list) or not 1 <= len(reports) <= 10:
            raise ValueError(f"invalid reports: {path}")
        for report in reports:
            if report["schema"] != "nodexlm-benchmark-v1":
                raise ValueError(f"invalid schema: {path}")
            basis = report["rate_basis"]
            if basis not in ("native_decode", "end_to_end"):
                raise ValueError("unknown timing basis")
            rows = report["rows"]
            if not 1 <= len(rows) <= 11:
                raise ValueError("invalid row count")
            fingerprint = rows[0]["sample"]["fingerprint"]
            if not re.fullmatch(r"[0-9a-f]{64}", fingerprint):
                raise ValueError("invalid fingerprint")
            for row in rows:
                sample = row["sample"]
                rate = sample["tokens_per_second"]
                if (sample["kind"] != "benchmark" or sample["rate_basis"] != basis
                        or sample["fingerprint"] != fingerprint or not math.isfinite(rate)
                        or rate <= 0 or type(sample["tokens"]) is not int or sample["tokens"] <= 0
                        or not re.fullmatch(r"Host|Guest|Cluster(?: [1-9][0-9]?)?", row["target"])):
                    raise ValueError("invalid measurement")
                fields = [report["model"], report["profile"], report["measured_at"],
                          row["target"], basis, f"{rate:.2f}", str(sample["tokens"])]
                lines.append("<tr>" + "".join("<td>" + html.escape(str(v)) + "</td>" for v in fields) + "</tr>")
    body = "\n".join(lines) or '<tr><td colspan="7">No reviewed results yet.</td></tr>'
    return ('<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">'
            '<title>NodexLM Benchmarks</title><style>body{font:15px system-ui;background:#202327;color:#e1e5e9;margin:24px}'
            'main{max-width:1100px;margin:auto}div{overflow:auto}table{border-collapse:collapse;width:100%}'
            'th,td{text-align:left;padding:10px;border-bottom:1px solid #535e68}</style>'
            '<main><h1>NodexLM Benchmarks</h1><p>Reviewed submissions; not independently reproduced unless documented. '
            'Compare identical models and timing bases only.</p><div><table><thead><tr>'
            '<th>Model</th><th>Profile</th><th>Date</th><th>Target</th><th>Timing basis</th><th>Tokens/s</th><th>Tokens</th>'
            '</tr></thead><tbody>' + body + '</tbody></table></div></main></html>')


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--results", type=pathlib.Path, default=pathlib.Path(__file__).parent / "results")
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    page = render(args.results)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(page, encoding="utf-8")
