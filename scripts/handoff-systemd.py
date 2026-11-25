#!/usr/bin/env python3
"""Opt-in cold role migration for the stock Linux systemd units over trusted SSH."""
import argparse
import json
import ipaddress
import pathlib
import re
import shlex
import subprocess
import sys
import time
import urllib.parse

HOST = "nodexlm.service"
GUEST = "nodexlm-guest.service"


def remote(host, *argv, timeout=60, stdin=None):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@-]{0,254}", host):
        raise ValueError("use an SSH host alias or user@host")
    return subprocess.run(
        ["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
         "-o", "ConnectTimeout=10", "--", host, shlex.join(argv)],
        check=True, capture_output=True, text=True, timeout=timeout, input=stdin,
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


def checked_endpoint(endpoint):
    url = urllib.parse.urlsplit(endpoint)
    if (url.scheme != "https" or url.username or url.password or url.path not in ("", "/")
            or url.query or url.fragment or url.port != 8443 or not url.hostname):
        raise ValueError("use the new Host's HTTPS LAN IP with port 8443")
    address = ipaddress.ip_address(url.hostname)
    if not address.is_private or address.is_loopback or address.is_unspecified:
        raise ValueError("use a private non-loopback LAN IP")
    return endpoint.rstrip("/")


def guest_ready(host, node_id):
    # Run the admin request on the trusted Host over loopback. The bearer
    # token stays on that machine and is not in arguments, SSH output or logs.
    script = (
        "import json,subprocess,sys,urllib.request;"
        "token=subprocess.check_output(['/usr/local/bin/nodexlm','web-token','--dir','/var/lib/nodexlm'],text=True).strip();"
        "req=urllib.request.Request('http://127.0.0.1:8787/v1/nodes',headers={'Authorization':'Bearer '+token});"
        "data=urllib.request.urlopen(req,timeout=5).read(2097153);"
        "assert len(data)<=2097152;"
        "nodes=json.loads(data);"
        "print('ready' if any(n.get('id')==sys.argv[1] and n.get('status')=='online' "
        "and n.get('runtime',{}).get('rpc_ready') for n in nodes) else 'waiting')"
    )
    return remote(host, "sudo", "-n", "-u", "nodexlm", "python3", "-c", script, node_id) == "ready"


def rejoin_guests(old, new, endpoint, guests):
    endpoint = checked_endpoint(endpoint)
    if not 1 <= len(guests) <= 16 or len(set(guests)) != len(guests):
        raise ValueError("specify 1..16 distinct Guest SSH aliases")
    expect(old, HOST, "ActiveState", ("inactive",))
    expect(old, HOST, "UnitFileState", ("disabled",))
    expect(new, HOST, "ActiveState", ("active",))
    expect(new, GUEST, "ActiveState", ("inactive",))
    new_id = remote(new, "cat", "/etc/machine-id")
    if not new_id:
        raise RuntimeError("new Host machine identity missing")
    machine_ids = {new_id}
    for guest in guests:
        identity = remote(guest, "cat", "/etc/machine-id")
        if not identity or identity in machine_ids:
            raise ValueError("duplicate machine or new Host in Guest list")
        machine_ids.add(identity)
        expect(guest, HOST, "ActiveState", ("inactive",))
        expect(guest, GUEST, "LoadState", ("loaded",))
        expect(guest, GUEST, "UnitFileState", ("enabled", "disabled"))
    # Pin the new Host certificate independently over SSH, not from an
    # untrusted network offer. Rotate the temporary code for each Guest.
    fingerprint = remote(new, "sudo", "-n", "-u", "nodexlm", "openssl", "x509",
                         "-in", "/var/lib/nodexlm/server.crt", "-noout", "-sha256", "-fingerprint")
    pin = fingerprint.partition("=")[2].replace(":", "").lower()
    if not re.fullmatch(r"[0-9a-f]{64}", pin):
        raise RuntimeError("cannot verify new Host certificate SHA-256")
    completed = []
    for guest in guests:
        try:
            stop(guest, GUEST)
            enabled(guest, GUEST, "disabled")
            raw = remote(new, "sudo", "-n", "-u", "nodexlm", "/usr/local/bin/nodexlm",
                         "pairing", "--dir", "/var/lib/nodexlm")
            match = re.fullmatch(r"NODEXLM PAIRING CODE: ([A-HJ-NP-Z2-9]{8}) \\(valid [^)]+\\)", raw)
            if not match:
                raise RuntimeError("cannot obtain temporary pairing code")
            # Stdin only: code never appears in argv or printed diagnostics.
            result = remote(guest, "sudo", "-n", "-u", "nodexlm-guest", "/usr/local/bin/nodexlm",
                            "join", "--dir", "/var/lib/nodexlm-guest", "--server", endpoint,
                            "--fingerprint", pin, "--rejoin", "--code", "-", stdin=match.group(1)+"\n")
            node = re.search(r" ID ([A-Za-z0-9_-]{1,128})$", result)
            if not node:
                raise RuntimeError("pairing returned no node identity")
            start(guest, GUEST)
            for _ in range(60):
                if guest_ready(new, node.group(1)):
                    break
                time.sleep(2)
            else:
                raise RuntimeError("new Host did not report RPC-ready Guest")
            enabled(guest, GUEST, "enabled")
            completed.append(guest)
        except (Exception, KeyboardInterrupt) as error:
            # The join may have replaced credentials; do not start against the
            # old coordinator or automatically roll back a completed switch.
            try:
                enabled(guest, GUEST, "disabled")
                stop(guest, GUEST)
            except (Exception, KeyboardInterrupt):
                raise RuntimeError(f"rejoin failed on {guest}; cannot confirm Guest stopped; completed: {completed}") from error
            raise RuntimeError(f"rejoin failed on {guest}; Guest stopped; completed: {completed}; retry only remaining Guests") from error
    return completed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--old", required=True, help="old Host SSH alias")
    parser.add_argument("--new", required=True, help="target Guest SSH alias")
    parser.add_argument("--manifest", type=pathlib.Path)
    parser.add_argument("--rejoin-only", action="store_true", help="enroll Guests after a completed switch")
    parser.add_argument("--guest", action="append", default=[], help="Guest SSH alias (repeat)")
    parser.add_argument("--endpoint", help="new Host HTTPS LAN IP, port 8443")
    parser.add_argument("--confirm-rejoin", action="store_true")
    parser.add_argument("--verify-timeout", type=int, default=1800)
    parser.add_argument("--confirm-cold-switch", action="store_true")
    args = parser.parse_args()
    if args.rejoin_only:
        if not args.confirm_rejoin or not args.endpoint or not args.guest or args.confirm_cold_switch:
            parser.error("rejoin-only requires --confirm-rejoin, --endpoint and --guest; no cold-switch flag")
        print("RPC-ready Guests:", ", ".join(rejoin_guests(args.old, args.new, args.endpoint, args.guest)))
        return
    if args.guest or args.endpoint or args.confirm_rejoin:
        parser.error("Guest enrollment is a separate --rejoin-only operation")
    if args.manifest is None:
        parser.error("--manifest required for cold switch")
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
