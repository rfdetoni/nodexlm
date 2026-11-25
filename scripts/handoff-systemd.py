#!/usr/bin/env python3
"""Opt-in cold role migration for the stock Linux systemd units over trusted SSH."""
import argparse
import json
import pathlib
import re
import shlex
import subprocess
import sys
import time

HOST = "nodexlm.service"
GUEST = "nodexlm-guest.service"


def remote(host, *argv, timeout=60):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@-]{0,254}", host):
        raise ValueError("use an SSH host alias or user@host")
    return subprocess.run(
        ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
         "-o", "ConnectTimeout=10", "--", host, shlex.join(argv)],
        check=True, capture_output=True, text=True, timeout=timeout,
    ).stdout.strip()


def ctl(host, *args):
    return remote(host, "sudo", "-n", "systemctl", *args)


def state(host, unit, prop="ActiveState"):
    return ctl(host, "show", "--property=" + prop, "--value", unit)


def expect(host, unit, prop, expected):
    actual = state(host, unit, prop)
    if actual not in expected:
        raise RuntimeError(f"{host}: {unit} {prop}={actual}; expected {expected}")
    return actual


def enabled(host, unit, value):
    ctl(host, "enable" if value == "enabled" else "disable", unit)
    expect(host, unit, "UnitFileState", (value,))


def stop(host, unit):
    ctl(host, "stop", unit)
    expect(host, unit, "ActiveState", ("inactive",))


def start(host, unit):
    ctl(host, "start", unit)
    expect(host, unit, "ActiveState", ("active",))


def ready(host):
    # Readiness of the stock Web console, not a claim of model inference.
    for _ in range(30):
        try:
            expect(host, HOST, "ActiveState", ("active",))
            remote(host, "curl", "--fail", "--silent", "--show-error",
                   "--max-time", "3", "--output", "/dev/null",
                   "http://127.0.0.1:8787/")
            return
        except (RuntimeError, subprocess.SubprocessError):
            time.sleep(2)
    raise RuntimeError("new Host web console not ready")


def verify_manifest(host, manifest, timeout):
    model = manifest.get("model")
    if not isinstance(model, str) or not model.lower().endswith(".gguf") or not re.fullmatch(r"[^/\\:\x00-\x1f\x7f]{1,180}", model):
        raise ValueError("manifest model must be a safe GGUF basename")
    actual = json.loads(remote(host, "sudo", "-n", "-u", "nodexlm",
                               "/usr/local/bin/nodexlm", "handoff", "manifest",
                               "--source", "/var/lib/nodexlm/models/" + model,
                               timeout=timeout))
    if actual != manifest:
        raise RuntimeError("staged GGUF digest mismatch")


def switch(old, new, manifest, verify_timeout=1800):
    if old == new or remote(old, "cat", "/etc/machine-id") == remote(new, "cat", "/etc/machine-id"):
        raise ValueError("old and new must be different physical systems")
    expect(old, HOST, "ActiveState", ("active",))
    expect(new, GUEST, "ActiveState", ("active",))
    expect(new, HOST, "ActiveState", ("inactive",))
    expect(new, HOST, "UnitFileState", ("disabled",))
    old_enable = expect(old, HOST, "UnitFileState", ("enabled", "disabled"))
    guest_enable = expect(new, GUEST, "UnitFileState", ("enabled", "disabled"))
    verify_manifest(new, manifest, verify_timeout)
    # No mutation before all preflight checks finish.
    try:
        enabled(old, HOST, "disabled")
        stop(old, HOST)
        enabled(new, GUEST, "disabled")
        stop(new, GUEST)
        start(new, HOST)
        ready(new)
        enabled(new, HOST, old_enable)
    except (Exception, KeyboardInterrupt) as original:
        # Never restart the old coordinator unless the destination is
        # positively confirmed stopped and disabled.
        try:
            enabled(new, HOST, "disabled")
            stop(new, HOST)
        except (Exception, KeyboardInterrupt) as rollback:
            raise RuntimeError("rollback blocked: cannot fence new Host; do not restart old Host until manually verified stopped") from rollback
        errors = []
        for host, unit, value in ((old, HOST, old_enable), (new, GUEST, guest_enable)):
            try:
                enabled(host, unit, value)
                start(host, unit)
            except (Exception, KeyboardInterrupt) as rollback:
                errors.append(f"{host}/{unit}: {rollback}")
        if errors:
            raise RuntimeError("rollback incomplete: " + "; ".join(errors)) from original
        raise RuntimeError("handoff failed; original services restored") from original


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--old", required=True, help="old Host SSH alias")
    parser.add_argument("--new", required=True, help="target Guest SSH alias")
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--verify-timeout", type=int, default=1800)
    parser.add_argument("--confirm-cold-switch", action="store_true")
    args = parser.parse_args()
    if not args.confirm_cold_switch:
        parser.error("requires --confirm-cold-switch; stops running services")
    if not 60 <= args.verify_timeout <= 86400:
        parser.error("verify timeout must be 60..86400 seconds")
    with args.manifest.open("rb") as file:
        raw = file.read(65537)
    if len(raw) > 65536:
        parser.error("manifest exceeds limit")
    manifest = json.loads(raw)
    switch(args.old, args.new, manifest, args.verify_timeout)
    print("Cold switch complete; new Host console ready. Verify inference and re-pair Guests using the new identity.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"handoff: {error}", file=sys.stderr)
        sys.exit(1)
