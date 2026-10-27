#!/usr/bin/env python3
"""Bounded source/public-install validation; receipts contain generated evidence only.

Local heavy runs must enter the shared test queue before invoking this script.
The public-install mode uses public HTTPS module sources and brand-new caches,
never the checkout or caller's installed ARM, module cache or backend.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time

MODULE = "github.com/inferal-oss/arm"
ROOT = Path(__file__).resolve().parent.parent


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def bind_public_source(resolved, expected_commit):
    """Require the full public origin to match the checked-out source identity."""
    actual = (resolved.get("Origin") or {}).get("Hash")
    valid_expected = len(expected_commit) == 40 and all(c in "0123456789abcdef" for c in expected_commit)
    return {"expected_source_commit": expected_commit, "resolved_source_commit": actual,
            "matches": valid_expected and actual == expected_commit}


def stop_owned_child(process):
    """Signal the Popen-owned child once, then bound its original wait.

    Popen.kill checks its child status, including an already reaped leader.
    Never signal a numeric process group: an earlier wait may have reaped its
    leader and allowed that group ID to be reused. Descendants are unproven;
    the hosted runner must dispose of its job on timeout/interruption.
    """
    receipt = {"scope": "original_child_only", "descendants": "unproven", "original_child_reaped": False}
    try:
        process.kill()
        receipt["returncode"] = process.wait(timeout=5)
        receipt["original_child_reaped"] = True
    except (OSError, subprocess.TimeoutExpired) as error:
        receipt["error"] = str(error)
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--public-install", metavar="QUERY")
    parser.add_argument("--scope", choices=("full", "assets"), default="full", help="assets renews only changed browser embedding/HTTP tests; prior unchanged-source evidence is separate")
    args = parser.parse_args()
    go = shutil.which(args.go)
    if not go:
        parser.error("Go executable unavailable")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, GOMAXPROCS="1", GOFLAGS="-p=1", GOTOOLCHAIN="local", GOWORK="off", GOENV="off")
    commands = []
    failures = []

    # Hosted cancellation stops and waits for the original child. Descendants
    # are not proved stopped, so aborted runs cannot establish cleanup success.
    def interrupted(signum, frame):
        raise KeyboardInterrupt("validation interrupted by signal " + str(signum))

    signal.signal(signal.SIGTERM, interrupted)
    if hasattr(signal, "SIGHUP"):
        signal.signal(signal.SIGHUP, interrupted)

    def run(name, command, cwd=ROOT, extra=None, timeout=1200):
        print("START", name, flush=True)
        started = time.monotonic()
        interruption = None
        cleanup = None
        with (output / (name + ".stdout.txt")).open("wb") as stdout, (output / (name + ".stderr.txt")).open("wb") as stderr:
            try:
                process = subprocess.Popen(command, cwd=cwd, env=env | (extra or {}), stdout=stdout, stderr=stderr, start_new_session=True)
                try:
                    code = process.wait(timeout=timeout)
                except subprocess.TimeoutExpired:
                    cleanup = stop_owned_child(process)
                    code = 124
                    interruption = TimeoutError("command timed out; descendant cleanup is unproven; refusing further commands")
                except BaseException as error:
                    cleanup = stop_owned_child(process)
                    code = cleanup.get("returncode")
                    interruption = error
            except OSError as error:
                stderr.write(str(error).encode())
                code = 127
        receipt = dict(name=name, argv=command, exit=code, seconds=round(time.monotonic()-started, 3), environment=extra or {})
        if cleanup is not None:
            receipt["cleanup"] = cleanup
            receipt["runner_aborted"] = True
        commands.append(receipt)
        (output / "commands.json").write_text(json.dumps(commands, indent=2) + "\n")
        if code or interruption is not None:
            failures.append(name)
        print("END", name, "exit", code, flush=True)
        if interruption is not None:
            raise interruption
        return code

    def require(ok, message):
        if not ok:
            failures.append(message)
            print("FAIL", message, flush=True)

    run("source-commit", ["git", "rev-parse", "HEAD"])
    source_commit = (output / "source-commit.stdout.txt").read_text().strip()
    if os.environ.get("GITHUB_SHA"):
        require(source_commit == os.environ["GITHUB_SHA"], "checkout differs from the immutable hosted workflow commit")
    run("source-tree", ["git", "rev-parse", "HEAD^{tree}"])
    run("source-status", ["git", "status", "--porcelain"])
    require(not (output / "source-status.stdout.txt").read_text().strip(), "source is dirty")
    run("toolchain", [go, "version"])
    tracked = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode().split("\0")
    manifest = [{"path": name, "sha256": digest(ROOT / name), "bytes": (ROOT / name).stat().st_size} for name in tracked if name]
    (output / "source-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")

    if args.public_install:
        identity = dict(bind_public_source({}, source_commit), query=args.public_install)
        (output / "public-source-identity.json").write_text(json.dumps(identity, indent=2) + "\n")
        require(args.public_install == "latest" or (len(args.public_install) == 40 and all(c in "0123456789abcdef" for c in args.public_install)), "query must be latest or an immutable full commit")
        if not failures:
            with tempfile.TemporaryDirectory(prefix="arm-public-install-") as scratch:
                temporary = Path(scratch)
                work = temporary / "work"
                work.mkdir()
                private = dict(GOBIN=str(temporary / "bin"), GOMODCACHE=str(temporary / "modules"), GOCACHE=str(temporary / "build"),
                               GOPATH=str(temporary / "gopath"), GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOPRIVATE="", GONOPROXY="", GONOSUMDB="",
                               GIT_TERMINAL_PROMPT="0", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GOINSECURE="", GO111MODULE="on")
                require(all(not Path(private[key]).exists() for key in ("GOBIN", "GOMODCACHE", "GOCACHE")), "public caches were not empty")
                run("public-environment", [go, "env", "-json", "GOBIN", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOSUMDB", "GOWORK", "GOENV", "GOTOOLCHAIN", "GOPATH", "CGO_ENABLED", "GOINSECURE", "GO111MODULE"], work, private)
                resolved = None
                code = run("public-query", [go, "mod", "download", "-json", MODULE + "@" + args.public_install], work, private)
                if not code:
                    resolved = json.loads((output / "public-query.stdout.txt").read_text())
                    require(resolved.get("Path") == MODULE and bool(resolved.get("Sum")) and bool(resolved.get("GoModSum")), "public module identity/sums missing")
                    identity = dict(bind_public_source(resolved, source_commit), query=args.public_install)
                    (output / "public-source-identity.json").write_text(json.dumps(identity, indent=2) + "\n")
                    require(identity["matches"], "public query origin differs from the checked-out source commit")
                    if args.public_install != "latest":
                        require(resolved.get("Origin", {}).get("Hash") == args.public_install, "public query resolved to a different source commit")
                    downloaded = Path(resolved["Dir"])
                    # Compare the actual public ZIP closure with the reviewed
                    # checkout; the checkout supplies expected hashes only and
                    # is never the install/build working directory or source.
                    closure = ["go.mod", "go.sum", "third_party/rsyncgo/LICENSE",
                               "third_party/rsyncgo/ARM-CHANGES.md",
                               "third_party/rsyncgo/UPSTREAM-go.mod.txt",
                               "third_party/rsyncgo/UPSTREAM-go.sum.txt",
                               "third_party/rsyncgo/rsyncstream/stream.go",
                               "third_party/rsyncgo/internal/sender/sender.go",
                               "third_party/rsyncgo/internal/receiver/receiver.go"]
                    closure += ["internal/server/web/novnc/core/inflator.js", "internal/server/web/novnc/core/deflator.js", "internal/server/web/novnc/LICENSE.txt"]
                    closure += [str(path.relative_to(ROOT)) for path in sorted((ROOT / "internal/server/web/novnc/deps/pako").rglob("*")) if path.is_file()]
                    hashes = {}
                    for name in closure:
                        actual = downloaded / name
                        require(actual.is_file() and actual.read_bytes() == (ROOT / name).read_bytes(), "public module ZIP differs at " + name)
                        if actual.is_file():
                            hashes[name] = digest(actual)
                    (output / "public-source-closure.json").write_text(json.dumps(hashes, indent=2, sort_keys=True) + "\n")
                    require(not (downloaded / "third_party/rsyncgo/go.mod").exists(), "public fork still has active nested module")
                # The original installation command executes even if the prior
                # query failed, preserving its own real public error/exit.
                code = run("public-install", [go, "install", MODULE + "/cmd/arm@" + args.public_install], work, private, timeout=1500)
                if not code:
                    binary = Path(private["GOBIN"]) / "arm"
                    (output / "installed-binary.json").write_text(json.dumps({"sha256": digest(binary), "bytes": binary.stat().st_size}, indent=2) + "\n")
                    run("installed-buildinfo", [go, "version", "-m", str(binary)], work, private)
                    info = (output / "installed-buildinfo.stdout.txt").read_text()
                    require("path\t" + MODULE + "/cmd/arm" in info and "mod\t" + MODULE + "\t" in info, "installed buildinfo has wrong module")
                    if resolved is not None:
                        module_records = [line.strip().split("\t") for line in info.splitlines() if line.strip().startswith("mod\t")]
                        expected = ["mod", MODULE, resolved.get("Version", ""), resolved.get("Sum", "")]
                        require(expected in module_records, "installed version/checksum differ from the recorded public query")
                    require("github.com/gokrazy/rsync\t" not in info and "=>" not in info, "installed buildinfo has replacement/upstream fork dependency")
                    public_environment = json.loads((output / "public-environment.stdout.txt").read_text())
                    require("build\tCGO_ENABLED=" + public_environment["CGO_ENABLED"] in info, "public installation changed the normal CGo environment")
                    home = temporary / "state"
                    for name, command in [("installed-version", ["version"]), ("installed-help", ["help"]), ("installed-empty-catalog", ["mesh", "artifacts", "list"])]:
                        run(name, [str(binary), "--home", str(home)] + command, work, private)
                    # Piped CLI output is JSON by default; this command has
                    # no local --json flag. Preserve command failure receipts
                    # rather than aborting while trying to decode empty output.
                    try:
                        catalog = json.loads((output / "installed-empty-catalog.stdout.txt").read_text())
                    except json.JSONDecodeError:
                        catalog = None
                    require(catalog == [], "thin install has unexpected local catalog or invalid output")
                    require(not home.exists(), "read-only thin probes created backend state")
            (output / "cleanup.json").write_text(json.dumps({"disposable_root_removed": not Path(scratch).exists(), "user_install_or_backend_modified": False}, indent=2) + "\n")
    elif args.scope == "assets":
        run("browser-asset-closure", [go, "test", "-mod=readonly", "-p=1", "-race", "-count=1", "-json", "-timeout=2m", "-run=^(TestScreenAssetsPreserveComposedDashboardModules|TestScreenEmbeddedAssetsSurviveModuleZipLayout)$", "./internal/server"])
        run("final-source-status", ["git", "status", "--porcelain"])
        require(not (output / "final-source-status.stdout.txt").read_text().strip(), "validation changed source")
    else:
        run("module-tidy", [go, "mod", "tidy", "-diff"])
        run("module-verify", [go, "mod", "verify"])
        run("full-compile", [go, "test", "-mod=readonly", "-p=1", "-json", "-timeout=15m", "-run=^$", "./..."])
        scoped = ["./internal/buildinfo", "./internal/companions", "./internal/rsyncengine", "./internal/distribution", "./scripts/build-bundle", "./third_party/rsyncgo/rsyncstream", "./third_party/rsyncgo/internal/sender", "./third_party/rsyncgo/internal/receiver"]
        run("scoped-race", [go, "test", "-mod=readonly", "-p=1", "-race", "-count=1", "-json", "-timeout=10m"] + scoped)
        run("fork-tests", [go, "test", "-mod=readonly", "-p=1", "-count=1", "-json", "-timeout=10m", "./third_party/rsyncgo/..."])
        run("browser-asset-closure", [go, "test", "-mod=readonly", "-p=1", "-race", "-count=1", "-json", "-timeout=2m", "-run=^(TestScreenAssetsPreserveComposedDashboardModules|TestScreenEmbeddedAssetsSurviveModuleZipLayout)$", "./internal/server"])
        run("scoped-vet", [go, "vet", "-mod=readonly", "-p=1", "./internal/buildinfo", "./internal/companions", "./internal/rsyncengine", "./internal/distribution", "./scripts/build-bundle", "./third_party/rsyncgo/..."])
        run("trimpath-layout", [go, "test", "-mod=readonly", "-p=1", "-trimpath", "-count=1", "-json", "-run=^TestInstallableModuleIncludesAuditedFork$", "./internal/distribution"])
        run("rsync-version", ["rsync", "--version"])
        run("native-build", [go, "build", "-mod=readonly", "-p=1", "-o", str(output.parent / "module-native"), "./cmd/arm"], extra={"CGO_ENABLED": "0"})
        run("linux-arm64-build", [go, "build", "-mod=readonly", "-p=1", "-o", str(output.parent / "module-linux-arm64"), "./cmd/arm"], extra={"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "arm64"})
        run("dependencies", [go, "list", "-mod=readonly", "-deps", "-json", "./cmd/arm"])
        run("final-source-status", ["git", "status", "--porcelain"])
        require(not (output / "final-source-status.stdout.txt").read_text().strip(), "validation changed source")
        for name in ("module-native", "module-linux-arm64"):
            binary = output.parent / name
            if binary.exists():
                run(name + "-buildinfo", [go, "version", "-m", str(binary)])
                binary.unlink()

    rosters = {}
    for name in ("full-compile", "scoped-race", "fork-tests", "trimpath-layout", "browser-asset-closure"):
        path = output / (name + ".stdout.txt")
        if path.exists():
            events = [json.loads(line) for line in path.read_text().splitlines() if line.startswith("{")]
            rosters[name] = {action: [{"package": event.get("Package"), "test": event.get("Test")} for event in events if event.get("Action") == action] for action in ("pass", "fail", "skip", "build-fail")}
    (output / "rosters.json").write_text(json.dumps(rosters, indent=2) + "\n")
    (output / "result.json").write_text(json.dumps({"failures": failures, "command_count": len(commands), "public_query": args.public_install, "scope": "public-install" if args.public_install else args.scope}, indent=2) + "\n")
    print("Failures:", failures, flush=True)
    return bool(failures)


if __name__ == "__main__":
    raise SystemExit(main())
