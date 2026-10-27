#!/usr/bin/env python3
"""Reconcile one tag's draft and verified assets, then publish without clobbering."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.parse

from common import digest, verify_directory, version


class GitHub:
    def __init__(self, repo):
        if repo != "inferal-oss/arm":
            raise ValueError("release publication is scoped to inferal-oss/arm")
        self.repo = repo

    def api(self, method, endpoint, payload=None, absent=False):
        args = ["gh", "api", "--method", method, endpoint]
        raw = None
        if payload is not None:
            args += ["--input", "-"]
            raw = json.dumps(payload).encode()
        try:
            result = subprocess.run(args, input=raw, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
        except subprocess.TimeoutExpired as error:
            raise RuntimeError("GitHub release API outcome unknown; inspect before retrying") from error
        if result.returncode:
            if absent and b"HTTP 404" in result.stderr:
                return None
            raise RuntimeError("GitHub release API request failed; inspect the tag before retrying")
        return json.loads(result.stdout) if result.stdout else None

    def release(self, tag):
        return self.api("GET", f"repos/{self.repo}/releases/tags/" + urllib.parse.quote(tag, safe=""), absent=True)

    def tag_commit(self, tag):
        record = self.api("GET", f"repos/{self.repo}/git/ref/tags/" + urllib.parse.quote(tag, safe=""))["object"]
        for _ in range(8):
            sha = record.get("sha", "")
            if not isinstance(sha, str) or len(sha) != 40 or any(c not in "0123456789abcdef" for c in sha):
                raise RuntimeError("invalid remote tag identity")
            if record.get("type") == "commit":
                return sha
            if record.get("type") != "tag":
                break
            record = self.api("GET", f"repos/{self.repo}/git/tags/{sha}")["object"]
        raise RuntimeError("remote tag does not resolve to a bounded commit identity")

    def assets(self, release):
        result = []
        for page in range(1, 12):
            batch = self.api("GET", f"repos/{self.repo}/releases/{release['id']}/assets?per_page=100&page={page}")
            result += batch
            if len(batch) < 100:
                return result
        raise RuntimeError("too many release assets")

    def matches(self, asset, path):
        if asset.get("state") != "uploaded" or asset.get("size") != path.stat().st_size:
            return False
        # Independently hash actual server bytes; no assumed API digest support.
        with tempfile.TemporaryDirectory(prefix="arm-release-check-") as temp:
            remote = Path(temp) / "asset"
            with remote.open("wb") as output:
                result = subprocess.run(["gh", "api", f"repos/{self.repo}/releases/assets/{asset['id']}", "-H", "Accept: application/octet-stream"], stdout=output, stderr=subprocess.PIPE, timeout=300)
            if result.returncode:
                raise RuntimeError("cannot verify existing release asset; no overwrite attempted")
            return remote.stat().st_size == path.stat().st_size and digest(remote) == digest(path)

    def create(self, tag, commit):
        notes = f"ARM {tag}\n\nSource: {commit}\n\nFour native macOS/Linux carriers (amd64 and arm64). Each carrier embeds all four platform payloads. Archives include catalog metadata and full license notices. Verify SHA256SUMS before installation.\n"
        try:
            return self.api("POST", f"repos/{self.repo}/releases", {"tag_name": tag, "target_commitish": commit, "name": "ARM " + tag, "body": notes, "draft": True, "prerelease": "-" in version(tag).split("+")[0]})
        except RuntimeError:
            # A lost response does not establish failure or authorize a second create.
            existing = self.release(tag)
            if existing is None:
                raise
            return existing

    def upload(self, tag, path):
        # Deliberately never use --clobber: an existing name is reconciled below.
        try:
            subprocess.run(["gh", "release", "upload", tag, str(path), "--repo", self.repo], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=300, check=False)
        except subprocess.TimeoutExpired:
            pass  # Reconciliation reads the exact name and bytes before any retry.

    def publish(self, release):
        try:
            return self.api("PATCH", f"repos/{self.repo}/releases/{release['id']}", {"draft": False, "make_latest": "legacy"})
        except RuntimeError:
            # Resolve an unknown publication outcome with a read, never a second write.
            current = self.api("GET", f"repos/{self.repo}/releases/{release['id']}")
            if current.get("draft"):
                raise
            return current


def reconcile(github, tag, commit, files):
    if github.tag_commit(tag) != commit:
        raise RuntimeError("remote tag differs from built source; no files changed")
    desired = {path.name: path for path in files}
    release = github.release(tag)
    if release is None:
        release = github.create(tag, commit)
    if release.get("tag_name") != tag:
        raise RuntimeError("release tag differs from requested version")
    original = github.assets(release)
    if len({asset["name"] for asset in original}) != len(original) or any(asset["name"] not in desired for asset in original):
        raise RuntimeError("release contains duplicate or unexpected assets; no files changed")
    for asset in original:
        if not github.matches(asset, desired[asset["name"]]):
            raise RuntimeError("existing release asset differs: " + asset["name"] + "; no files changed")
    if not release.get("draft") and {asset["name"] for asset in original} != set(desired):
        raise RuntimeError("published release is incomplete; no assets changed")
    for path in files:
        assets = github.assets(release)
        if len({asset["name"] for asset in assets}) != len(assets) or any(asset["name"] not in desired for asset in assets):
            raise RuntimeError("release contains duplicate or unexpected assets; no files changed")
        existing = next((asset for asset in assets if asset["name"] == path.name), None)
        if existing:
            if not github.matches(existing, path):
                raise RuntimeError("existing release asset differs: " + path.name + "; no overwrite attempted")
            continue
        if not release.get("draft"):
            raise RuntimeError("published release is incomplete; no assets changed")
        github.upload(tag, path)
        # A failure or lost upload reply is resolved by exact-name read and byte hash.
        assets = github.assets(release)
        found = [asset for asset in assets if asset["name"] == path.name]
        if len(found) != 1 or not github.matches(found[0], path):
            raise RuntimeError("asset upload outcome unresolved: " + path.name + "; draft retained")
    # Recheck the complete server set, not just files added during this attempt.
    assets = github.assets(release)
    if len(assets) != len(desired) or {asset["name"] for asset in assets} != set(desired):
        raise RuntimeError("release asset set changed; draft retained")
    for asset in assets:
        if not github.matches(asset, desired[asset["name"]]):
            raise RuntimeError("final release asset verification failed; draft retained")
    current = github.release(tag)
    if current is None or current["id"] != release["id"]:
        raise RuntimeError("release identity changed; no publication attempted")
    if github.tag_commit(tag) != commit:
        raise RuntimeError("remote tag changed during upload; draft retained")
    if current.get("draft"):
        current = github.publish(current)
    if current.get("draft") or current.get("tag_name") != tag:
        raise RuntimeError("release publication is unconfirmed")
    return current


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--directory", type=Path, required=True)
    args = parser.parse_args()
    tag = args.tag
    version(tag)
    if os.environ.get("GITHUB_EVENT_NAME") != "push" or os.environ.get("GITHUB_REF") != "refs/tags/" + tag:
        raise RuntimeError("only a version-tag push may publish")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if os.environ.get("GITHUB_SHA") != commit:
        raise RuntimeError("publication checkout differs from workflow source")
    if subprocess.check_output(["git", "rev-parse", "refs/tags/" + tag + "^{commit}"], text=True).strip() != commit:
        raise RuntimeError("local tag differs from built source")
    subprocess.run(["git", "merge-base", "--is-ancestor", commit, "origin/main"], check=True)
    remote = subprocess.check_output(["git", "ls-remote", "origin", "refs/tags/" + tag, "refs/tags/" + tag + "^{}"], text=True)
    targets = dict(line.split()[::-1] for line in remote.splitlines())
    if targets.get("refs/tags/" + tag + "^{}", targets.get("refs/tags/" + tag)) != commit:
        raise RuntimeError("remote tag changed after build; no publication attempted")
    _, files = verify_directory(args.directory, tag, commit)
    result = reconcile(GitHub(os.environ.get("GITHUB_REPOSITORY")), tag, commit, files)
    print("Published verified carrier release: " + result["html_url"])


if __name__ == "__main__":
    main()
