"""Failure-path tests operate on bounded fixtures and a fake release service."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch



import build as release_build
from common import PLATFORMS, digest, filenames, verify_directory, version
from publish import GitHub, reconcile

TAG = "v1.2.3"
COMMIT = "a" * 40


def fixture(directory):
    carriers = []
    for platform, name in zip(PLATFORMS, filenames(TAG)):
        path = directory / name
        path.write_bytes(("carrier fixture " + platform).encode())
        payloads = [{"os": p.split("-")[0], "arch": p.split("-")[1], "name": "arm-" + p, "size_bytes": 10, "compressed_size_bytes": 8, "sha256": "b" * 64, "compressed_sha256": "c" * 64} for p in PLATFORMS]
        carriers.append({"platform": platform, "archive": name, "size_bytes": path.stat().st_size, "sha256": digest(path), "embedded_catalog": {"schema": 1, "integrity_scope": "fixture metadata only", "expected_arm_version": "1.2.3", "expected_lifecycle_protocol": 1, "artifacts": payloads}})
    (directory / "release.json").write_text(json.dumps({"schema": 1, "tag": TAG, "version": "1.2.3", "source_commit": COMMIT, "lifecycle_protocol": 1, "carriers": carriers}))
    sums(directory)
    return verify_directory(directory, TAG, COMMIT)[1]


def sums(directory):
    (directory / "SHA256SUMS").write_text("".join(f"{digest(path)}  {path.name}\n" for path in sorted(directory.iterdir()) if path.name != "SHA256SUMS"))


class Fake:
    def __init__(self, files, *, draft=True, present=True):
        self.item = {"id": 9, "tag_name": TAG, "draft": draft, "html_url": "https://github.com/inferal-oss/arm/releases/tag/" + TAG} if present else None
        self.data = {p.name: p.read_bytes() for p in files}
        self.uploads = []
        self.publishes = 0
        self.creates = 0

    def release(self, tag):
        return self.item

    def create(self, tag, commit):
        self.creates += 1
        self.item = {"id": 9, "tag_name": TAG, "draft": True, "html_url": "https://github.com/inferal-oss/arm/releases/tag/" + TAG}
        return self.item

    def tag_commit(self, tag):
        return COMMIT

    def assets(self, release):
        return [{"name": name} for name in self.data]

    def matches(self, asset, path):
        return self.data.get(asset["name"]) == path.read_bytes()

    def upload(self, tag, path):
        self.uploads.append(path.name)
        self.data[path.name] = path.read_bytes()
        # Returning no reply models an upload whose outcome must be read back.

    def publish(self, release):
        self.publishes += 1
        self.item = {**release, "draft": False}
        return self.item


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.files = fixture(self.root)

    def test_semantic_tags_reject_paths_shell_aliases_and_bad_identifiers(self):
        for tag in ["v1", "1.2.3", "v01.2.3", "v1.2.3-01", "v1.2.3-", "v1.2.3-a..b", "v1.2.3+..", "v1.2.3/escape", "v1.2.3;command", "v1.2.3\n", "v1.2.3+" + "a" * 100]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                version(tag)
        for tag in [TAG, "v0.0.0-ci", "v1.2.3-rc.2+build.01"]:
            self.assertEqual(version(tag), tag[1:])

    def test_layout_binds_source_and_bytes(self):
        with self.assertRaises(ValueError):
            verify_directory(self.root, TAG, "d" * 40)
        (self.root / filenames(TAG)[0]).write_bytes(b"changed")
        with self.assertRaises(ValueError):
            verify_directory(self.root, TAG, COMMIT)

    def test_catalog_requires_four_payloads_in_every_carrier(self):
        path = self.root / "release.json"
        raw = json.loads(path.read_text())
        raw["carriers"][2]["embedded_catalog"]["artifacts"].pop()
        path.write_text(json.dumps(raw))
        sums(self.root)
        with self.assertRaises(ValueError):
            verify_directory(self.root, TAG, COMMIT)

    def test_extra_files_and_symlink_are_refused(self):
        extra = self.root / "backend-state"
        extra.write_bytes(b"must not upload")
        with self.assertRaises(ValueError):
            verify_directory(self.root, TAG, COMMIT)
        extra.unlink()
        path = self.root / filenames(TAG)[0]
        path.unlink()
        path.symlink_to("release.json")
        with self.assertRaises(ValueError):
            verify_directory(self.root, TAG, COMMIT)

    def test_first_publication_and_repeated_completion_do_not_duplicate(self):
        github = Fake([], present=False)
        result = reconcile(github, TAG, COMMIT, self.files)
        self.assertFalse(result["draft"])
        self.assertEqual((github.creates, github.publishes, len(github.uploads)), (1, 1, 6))
        reconcile(github, TAG, COMMIT, self.files)
        self.assertEqual((github.creates, github.publishes, len(github.uploads)), (1, 1, 6))

    def test_partial_draft_only_uploads_missing_bytes(self):
        github = Fake(self.files[:2])
        reconcile(github, TAG, COMMIT, self.files)
        self.assertEqual(set(github.uploads), {p.name for p in self.files[2:]})
        self.assertEqual(github.publishes, 1)

    def test_conflicting_existing_asset_refuses_all_writes(self):
        github = Fake(self.files[2:])
        github.data[self.files[-1].name] = b"different"
        with self.assertRaises(RuntimeError):
            reconcile(github, TAG, COMMIT, self.files)
        self.assertEqual((github.uploads, github.publishes), ([], 0))

    def test_published_incomplete_or_extra_set_refuses_all_writes(self):
        for github in [Fake(self.files[:2], draft=False), Fake(self.files)]:
            if github.item["draft"]:
                github.data["unrelated"] = b"do not delete"
            with self.assertRaises(RuntimeError):
                reconcile(github, TAG, COMMIT, self.files)
            self.assertEqual((github.uploads, github.publishes), ([], 0))

    def test_tag_move_during_upload_preserves_draft(self):
        github = Fake([])
        with patch.object(github, "tag_commit", side_effect=[COMMIT, "d" * 40]):
            with self.assertRaises(RuntimeError):
                reconcile(github, TAG, COMMIT, self.files)
        self.assertEqual(github.publishes, 0)
        self.assertTrue(github.item["draft"])

    def test_lost_create_reply_uses_exact_existing_release(self):
        github = GitHub("inferal-oss/arm")
        item = {"id": 42, "tag_name": TAG, "draft": True}
        with patch.object(github, "api", side_effect=RuntimeError("lost")) as api, patch.object(github, "release", return_value=item) as lookup:
            self.assertEqual(github.create(TAG, COMMIT), item)
            self.assertEqual(api.call_count, 1)
            lookup.assert_called_once_with(TAG)

    def test_lost_publish_reply_is_read_without_second_write(self):
        github = GitHub("inferal-oss/arm")
        item = {"id": 42, "tag_name": TAG, "draft": False}
        with patch.object(github, "api", side_effect=[RuntimeError("lost"), item]) as api:
            self.assertEqual(github.publish({**item, "draft": True}), item)
            self.assertEqual([call.args[0] for call in api.call_args_list], ["PATCH", "GET"])


class ReleaseCommandOwnershipTests(unittest.TestCase):
    def test_normal_exit_and_output(self):
        self.assertEqual(release_build.run([sys.executable, "-c", 'print("owned success")']), b"owned success\n")

    def test_exited_parent_with_pipe_holder_is_stopped(self):
        with tempfile.TemporaryDirectory(prefix="arm-release-group-test-") as directory:
            ready, finished = [Path(directory) / name for name in ("ready", "finished")]
            child = (
                "from pathlib import Path; import time; "
                f"Path({str(ready)!r}).write_text('ready'); "
                f"time.sleep(.6); Path({str(finished)!r}).write_text('continued')"
            )
            parent = (
                "from pathlib import Path; import subprocess, sys, time; "
                f"subprocess.Popen([sys.executable, '-c', {child!r}]); "
                f"path=Path({str(ready)!r})\n"
                "while not path.exists(): time.sleep(.005)\n"
            )
            with patch.object(release_build, "PROCESS_STOP_TIMEOUT", .1):
                with self.assertRaises(subprocess.TimeoutExpired):
                    release_build.run([sys.executable, "-c", parent], timeout=.2)
            self.assertTrue(ready.exists())
            time.sleep(.7)
            self.assertFalse(finished.exists())

    def test_live_timeout_escalates_and_joins(self):
        with tempfile.TemporaryDirectory(prefix="arm-release-timeout-test-") as directory:
            pidfile = Path(directory) / "pid"
            command = (
                "from pathlib import Path; import os, signal, time; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                f"Path({str(pidfile)!r}).write_text(str(os.getpid())); time.sleep(2)"
            )
            with patch.object(release_build, "PROCESS_STOP_TIMEOUT", .1):
                with self.assertRaises(subprocess.TimeoutExpired):
                    release_build.run([sys.executable, "-c", command], timeout=.2)
            with self.assertRaises(ProcessLookupError):
                os.kill(int(pidfile.read_text()), 0)

    def test_interrupt_after_kernel_reap_never_signals_again(self):
        import owned_process
        original = owned_process.os.waitpid
        def reap_then_interrupt(*args):
            original(*args)
            raise KeyboardInterrupt("after actual wait")
        with patch.object(owned_process.os, "waitpid", side_effect=reap_then_interrupt), \
                patch.object(owned_process.os, "killpg", side_effect=AssertionError("post-reap signal")):
            with self.assertRaisesRegex(owned_process.OwnershipError, "original_wait_unconfirmed"):
                release_build.run([sys.executable, "-c", "pass"])

    def test_already_reaped_original_closes_authority(self):
        import owned_process
        owner = owned_process.OwnedCommand([sys.executable, "-c", "pass"])
        try:
            owner.start()
            os.waitpid(owner.pid, 0)
            with patch.object(owned_process.os, "killpg", side_effect=AssertionError("stale group signal")):
                with self.assertRaisesRegex(owned_process.OwnershipError, "no_longer_waitable"):
                    owner.cancel()
            self.assertFalse(owner.authority)
        finally:
            owner.close_pipes()

    def test_sigterm_runs_owned_child_cleanup(self):
        with tempfile.TemporaryDirectory(prefix="arm-release-signal-test-") as directory:
            ready, finished = [Path(directory) / name for name in ("ready", "finished")]
            child = (
                "from pathlib import Path; import time; "
                f"Path({str(ready)!r}).write_text('ready'); "
                f"time.sleep(.6); Path({str(finished)!r}).write_text('continued')"
            )
            wrapper = (
                "import signal, sys; "
                f"sys.path.insert(0, {str(Path(__file__).resolve().parent)!r}); "
                "import build; signal.signal(signal.SIGTERM, build.interrupted); "
                f"build.run([sys.executable, '-c', {child!r}], timeout=3)"
            )
            process = subprocess.Popen([sys.executable, "-c", wrapper], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 2
                while not ready.exists() and time.monotonic() < deadline:
                    time.sleep(.005)
                self.assertTrue(ready.exists())
                process.send_signal(signal.SIGTERM)
                _, error = process.communicate(timeout=2)
                self.assertNotEqual(process.returncode, 0)
                self.assertIn(b"release_build_interrupted", error)
                time.sleep(.7)
                self.assertFalse(finished.exists())
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=2)


if __name__ == "__main__":
    unittest.main()
