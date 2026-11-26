#!/usr/bin/env python3
"""Test service switch ordering and fail-closed rollback without touching SSH."""
import importlib.util
import pathlib
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "handoff", pathlib.Path(__file__).with_name("handoff-systemd.py")
)
handoff = importlib.util.module_from_spec(spec)
spec.loader.exec_module(handoff)


class SwitchTests(unittest.TestCase):
    def run_switch(self, fail_ready=False, fail_fence=False, fail_manifest=False):
        calls = []
        host, guest = handoff.HOST, handoff.GUEST
        states = {
            ("old", host): ["active", "enabled"],
            ("new", host): ["inactive", "disabled"],
            ("new", guest): ["active", "enabled"],
        }

        def remote(node, *args, **kwargs):
            return node

        def ctl(node, action, *args):
            unit = args[-1]
            item = states[node, unit]
            if action == "show":
                return item[1 if "--property=UnitFileState" in args else 0]
            calls.append((node, action, unit))
            if fail_fence and node == "new" and unit == host and action == "stop":
                raise RuntimeError("network unavailable")
            if action in ("enable", "disable"):
                item[1] = action + "d"
            elif action in ("start", "stop"):
                item[0] = "active" if action == "start" else "inactive"
            if states["old", host][0] == states["new", host][0] == "active":
                raise AssertionError("split brain")

        def verify(*args):
            if fail_manifest:
                raise RuntimeError("invalid digest")

        def ready(*args):
            if fail_ready:
                raise RuntimeError("not ready")

        with patch.object(handoff, "remote", remote), patch.object(handoff, "ctl", ctl), \
                patch.object(handoff, "verify_manifest", verify), patch.object(handoff, "ready", ready):
            if fail_manifest:
                with self.assertRaisesRegex(RuntimeError, "invalid digest"):
                    handoff.switch("old", "new", {})
            elif fail_fence:
                with self.assertRaisesRegex(RuntimeError, "cannot fence"):
                    handoff.switch("old", "new", {})
            elif fail_ready:
                with self.assertRaisesRegex(RuntimeError, "original services restored"):
                    handoff.switch("old", "new", {})
            else:
                handoff.switch("old", "new", {})
        return calls, states

    def test_switch(self):
        calls, states = self.run_switch()
        self.assertLess(calls.index(("old", "stop", handoff.HOST)),
                        calls.index(("new", "start", handoff.HOST)))
        self.assertEqual(states["new", handoff.HOST], ["active", "enabled"])
        self.assertEqual(states["old", handoff.HOST], ["inactive", "disabled"])
        self.assertEqual(states["new", handoff.GUEST], ["inactive", "disabled"])

    def test_rollback(self):
        calls, states = self.run_switch(fail_ready=True)
        self.assertLess(calls.index(("new", "stop", handoff.HOST)),
                        calls.index(("old", "start", handoff.HOST)))
        self.assertEqual(states["old", handoff.HOST], ["active", "enabled"])
        self.assertEqual(states["new", handoff.GUEST], ["active", "enabled"])

    def test_unconfirmed_fence(self):
        calls, states = self.run_switch(fail_ready=True, fail_fence=True)
        self.assertNotIn(("old", "start", handoff.HOST), calls)
        self.assertEqual(states["old", handoff.HOST][0], "inactive")

    def test_invalid_manifest_no_mutation(self):
        calls, _ = self.run_switch(fail_manifest=True)
        self.assertEqual(calls, [])


class RejoinTests(unittest.TestCase):
    def test_endpoint(self):
        self.assertEqual(handoff.checked_endpoint("https://192.168.1.10:8443/"),
                         "https://192.168.1.10:8443")
        for bad in ("http://192.168.1.10:8443", "https://example.com:8443",
                    "https://127.0.0.1:8443", "https://192.168.1.10:443",
                    "https://user@192.168.1.10:8443"):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                handoff.checked_endpoint(bad)

    def run_rejoin(self, fail=False):
        calls = []

        def remote(node, *args, **kwargs):
            if args[0] == "cat":
                return node
            if "openssl" in args:
                return "sha256 Fingerprint=" + "AA:" * 31 + "AA"
            if "pairing" in args:
                return "NODEXLM PAIRING CODE: ABCD2345 (valid 10m)"
            if "join" in args:
                self.assertNotIn("ABCD2345", args)
                self.assertEqual(kwargs["stdin"], "ABCD2345\n")
                self.assertEqual(args[args.index("--fingerprint") + 1], "aa" * 32)
                calls.append(("join", node))
                if fail:
                    raise RuntimeError("join failed")
                return "Node paired securely: guest ID abc123"
            raise AssertionError(args)

        with patch.object(handoff, "remote", remote), \
                patch.object(handoff, "expect"), \
                patch.object(handoff, "stop", side_effect=lambda n, u: calls.append(("stop", n))), \
                patch.object(handoff, "start", side_effect=lambda n, u: calls.append(("start", n))), \
                patch.object(handoff, "enabled", side_effect=lambda n, u, v: calls.append((v, n))), \
                patch.object(handoff, "guest_ready", return_value=True):
            if fail:
                with self.assertRaisesRegex(RuntimeError, "Guest stopped"):
                    handoff.rejoin_guests("old", "new", "https://192.168.1.10:8443", ["old"])
            else:
                self.assertEqual(handoff.rejoin_guests("old", "new", "https://192.168.1.10:8443", ["old"]), ["old"])
        return calls

    def test_rejoin_order(self):
        self.assertEqual(self.run_rejoin(), [
            ("stop", "old"), ("disabled", "old"), ("join", "old"),
            ("start", "old"), ("enabled", "old"),
        ])

    def test_rejoin_failure_leaves_guest_stopped(self):
        self.assertEqual(self.run_rejoin(fail=True), [
            ("stop", "old"), ("disabled", "old"), ("join", "old"),
            ("disabled", "old"), ("stop", "old"),
        ])


if __name__ == "__main__":
    unittest.main()
