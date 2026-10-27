#!/usr/bin/env python3
"""Cheap ownership/identity regressions; no Go build, network or ARM state."""
import importlib.util
import hashlib
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("validate_module", Path(__file__).with_name("validate-module.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def git_origin(commit):
    return {"VCS": "git", "URL": module.REPOSITORY_URL, "Hash": commit}


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
        receipt = module.bind_public_source({"Origin": git_origin(expected)}, expected)
        self.assertEqual(receipt, {"expected_source_commit": expected, "resolved_source_commit": expected, "expected_repository_url": module.REPOSITORY_URL, "resolved_repository_url": module.REPOSITORY_URL, "expected_vcs": "git", "resolved_vcs": "git", "matches": True})

    def test_stale_latest_fails_even_with_identical_closure_files(self):
        expected = "a" * 40
        closure = {"asset-" + str(i): "unchanged-bytes" for i in range(26)}
        current = {"Origin": git_origin(expected), "closure": closure}
        stale = {"Origin": git_origin("b" * 40), "closure": dict(closure)}
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


class SelectedVersionOrigin(unittest.TestCase):
    def setUp(self):
        self.expected = "a" * 40
        self.latest = {"Path": module.MODULE, "Version": "v0.0.0-20261027000000-aaaaaaaaaaaa", "Sum": "h1:module", "GoModSum": "h1:gomod"}
        self.origin = dict(self.latest, Origin=git_origin(self.expected))

    def test_omitted_latest_origin_requires_matching_selected_version_provenance(self):
        receipt = module.bind_selected_public_source(self.latest, self.origin, self.expected)
        self.assertTrue(receipt["matches"])
        self.assertEqual(receipt["resolved_source_commit"], self.expected)
        self.assertTrue(receipt["selection_checksums_match"])

    def test_other_version_or_checksum_cannot_supply_origin(self):
        for field in ("Path", "Version", "Sum", "GoModSum"):
            with self.subTest(field=field):
                other = dict(self.origin); other[field] = "different"
                self.assertFalse(module.bind_selected_public_source(self.latest, other, self.expected)["matches"])

    def test_wrong_or_missing_origin_cannot_pass(self):
        for origin in ({}, {"Origin": {"Hash": "b" * 40}}, dict(self.latest)):
            with self.subTest(origin=origin):
                self.assertFalse(module.bind_selected_public_source(self.latest, origin, self.expected)["matches"])

    def test_conflicting_latest_origin_is_not_overridden(self):
        latest = dict(self.latest, Origin={"Hash": "b" * 40})
        self.assertFalse(module.bind_selected_public_source(latest, self.origin, self.expected)["matches"])


class VanityRepositoryIdentity(unittest.TestCase):
    def setUp(self):
        self.commit = "a" * 40
        self.latest = {"Path": module.MODULE, "Version": "v0.0.0-20261027000000-aaaaaaaaaaaa", "Sum": "h1:module", "GoModSum": "h1:gomod"}
        self.selected = dict(self.latest, Origin=git_origin(self.commit))

    def test_vanity_module_keeps_the_github_source_repository(self):
        self.assertEqual(module.MODULE, "getarm.org")
        self.assertEqual(module.REPOSITORY_URL, "https://github.com/inferal-oss/arm")
        self.assertTrue(module.bind_selected_public_source(self.latest, self.selected, self.commit)["matches"])

    def test_wrong_repository_is_refused_even_with_the_same_full_hash(self):
        for url in ("https://getarm.org", "https://github.com/other/arm", "http://github.com/inferal-oss/arm", None):
            with self.subTest(url=url):
                origin = git_origin(self.commit)
                if url is None:
                    del origin["URL"]
                else:
                    origin["URL"] = url
                self.assertFalse(module.bind_selected_public_source(self.latest, dict(self.selected, Origin=origin), self.commit)["matches"])

    def test_wrong_or_missing_vcs_is_refused(self):
        for vcs in ("hg", None):
            with self.subTest(vcs=vcs):
                origin = git_origin(self.commit)
                if vcs is None:
                    del origin["VCS"]
                else:
                    origin["VCS"] = vcs
                self.assertFalse(module.bind_selected_public_source(self.latest, dict(self.selected, Origin=origin), self.commit)["matches"])

    def test_old_module_path_cannot_borrow_the_new_module_provenance(self):
        old = dict(self.latest, Path="github.com/inferal-oss/arm")
        selected = dict(old, Origin=git_origin(self.commit))
        self.assertFalse(module.bind_selected_public_source(old, selected, self.commit)["matches"])

    def test_partial_query_origin_must_agree_with_selected_git_identity(self):
        for partial in ({}, {"Hash": self.commit}, {"URL": module.REPOSITORY_URL}, {"VCS": "git"}):
            with self.subTest(partial=partial):
                self.assertTrue(module.bind_selected_public_source(dict(self.latest, Origin=partial), self.selected, self.commit)["matches"])
        for conflict in ({"Hash": "b" * 40}, {"URL": "https://getarm.org"}, {"VCS": "hg"}):
            with self.subTest(conflict=conflict):
                self.assertFalse(module.bind_selected_public_source(dict(self.latest, Origin=conflict), self.selected, self.commit)["matches"])

    def test_expected_source_must_be_a_full_lowercase_git_commit(self):
        for expected in ("", "main", "a" * 39, "A" * 40, None):
            with self.subTest(expected=expected):
                self.assertFalse(module.bind_public_source(self.selected, expected)["matches"])


class SeparateSourceAndHarness(unittest.TestCase):
    def repository(self, root, files):
        root.mkdir()
        for name, contents in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(contents)
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        for args in (("-c", "init.templateDir=", "init", "-q"), ("add", "."),
                     ("-c", "core.hooksPath=" + os.devnull, "-c", "commit.gpgSign=false",
                      "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")):
            subprocess.run(["git", "-C", str(root), *args], env=env, check=True, capture_output=True)
        return subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], env=env).decode().strip()

    def test_clean_source_head_and_helper_identity_are_independent(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = root / "helper"
            selected = root / "selected"
            helper_commit = self.repository(helper, {"scripts/validate-module.py": "helper", "scripts/test-validate-module.py": "controls"})
            source_commit = self.repository(selected, {"source.txt": "selected public source"})
            with mock.patch.object(module, "ROOT", helper):
                identity = module.harness_identity()
                manifest = module.source_manifest(selected)
            self.assertNotEqual(helper_commit, source_commit)
            self.assertEqual(identity["commit"], helper_commit)
            self.assertEqual(identity["status"], "")
            self.assertEqual(identity["files"]["scripts/validate-module.py"], hashlib.sha256(b"helper").hexdigest())
            self.assertEqual(manifest, [{"path": "source.txt", "sha256": hashlib.sha256(b"selected public source").hexdigest(), "bytes": len(b"selected public source")}])
            public = {"Origin": git_origin(source_commit)}
            self.assertTrue(module.bind_public_source(public, source_commit)["matches"])
            self.assertFalse(module.bind_public_source(public, helper_commit)["matches"])

    def test_source_change_does_not_hide_behind_unchanged_harness(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            helper = root / "helper"
            selected = root / "selected"
            self.repository(helper, {"scripts/validate-module.py": "helper", "scripts/test-validate-module.py": "controls"})
            self.repository(selected, {"source.txt": "before"})
            with mock.patch.object(module, "ROOT", helper):
                helper_before = module.harness_identity()
                before = module.source_manifest(selected)
                (selected / "source.txt").write_text("changed after download")
                after = module.source_manifest(selected)
                self.assertEqual(module.harness_identity(), helper_before)
            self.assertNotEqual(after, before)


if __name__ == "__main__":
    unittest.main()
