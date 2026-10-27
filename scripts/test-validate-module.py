#!/usr/bin/env python3
"""Cheap ownership/identity regressions; no Go build, network or ARM state."""
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("validate_module", Path(__file__).with_name("validate-module.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class OwnedChildCleanup(unittest.TestCase):
    def test_reaped_child_never_signals_a_reused_group_or_pid(self):
        process = subprocess.Popen([sys.executable, "-c", "pass"], start_new_session=True)
        self.assertEqual(process.wait(timeout=5), 0)
        with mock.patch.object(os, "killpg", side_effect=AssertionError("unowned group signal")), mock.patch.object(os, "kill", side_effect=AssertionError("reaped PID signal")):
            receipt = module.stop_owned_child(process)
        self.assertTrue(receipt["original_child_reaped"])
        self.assertEqual(receipt["returncode"], 0)
        self.assertEqual(receipt["descendants"], "unproven")

    def test_live_original_child_gets_one_signal_and_is_reaped(self):
        process = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True)
        try:
            with mock.patch.object(os, "killpg", side_effect=AssertionError("numeric group signal")), mock.patch.object(os, "kill", wraps=os.kill) as kill:
                receipt = module.stop_owned_child(process)
            self.assertTrue(receipt["original_child_reaped"])
            self.assertEqual(receipt["returncode"], -signal.SIGKILL)
            self.assertEqual(kill.call_args_list, [mock.call(process.pid, signal.SIGKILL)])
            self.assertEqual(receipt["descendants"], "unproven")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)

    def test_failed_wait_retains_uncertainty_without_signal_escalation(self):
        process = mock.Mock()
        process.wait.side_effect = subprocess.TimeoutExpired("owned fixture", 5)
        with mock.patch.object(os, "killpg", side_effect=AssertionError("group signal")):
            receipt = module.stop_owned_child(process)
        self.assertEqual(process.mock_calls, [mock.call.kill(), mock.call.wait(timeout=5)])
        self.assertFalse(receipt["original_child_reaped"])
        self.assertEqual(receipt["descendants"], "unproven")
        self.assertNotIn("returncode", receipt)
        self.assertIn("error", receipt)


class PublicSourceBinding(unittest.TestCase):
    def test_current_main_origin_matches(self):
        expected = "a" * 40
        receipt = module.bind_public_source({"Origin": {"Hash": expected}}, expected)
        self.assertEqual(receipt, {"expected_source_commit": expected, "resolved_source_commit": expected, "matches": True})

    def test_stale_latest_fails_even_with_identical_closure_files(self):
        expected = "a" * 40
        closure = {"asset-" + str(i): "unchanged-bytes" for i in range(26)}
        current = {"Origin": {"Hash": expected}, "closure": closure}
        stale = {"Origin": {"Hash": "b" * 40}, "closure": dict(closure)}
        self.assertEqual(current["closure"], stale["closure"])
        self.assertTrue(module.bind_public_source(current, expected)["matches"])
        receipt = module.bind_public_source(stale, expected)
        self.assertFalse(receipt["matches"])
        self.assertEqual(receipt["expected_source_commit"], expected)
        self.assertEqual(receipt["resolved_source_commit"], "b" * 40)

    def test_missing_origin_cannot_pass(self):
        self.assertFalse(module.bind_public_source({}, "a" * 40)["matches"])

    def test_missing_source_identity_cannot_pass_by_empty_equality(self):
        self.assertFalse(module.bind_public_source({}, "")["matches"])


if __name__ == "__main__":
    unittest.main()
