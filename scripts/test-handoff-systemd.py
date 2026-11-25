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


if __name__ == "__main__":
    unittest.main()
