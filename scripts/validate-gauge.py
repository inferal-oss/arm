#!/usr/bin/env python3
"""Hosted-only, source-bound gauge verification; publishes generated receipts only."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
BRANCH = "refs/heads/codex/terminal-gauge-ci"
REPOSITORY = "inferal-oss/arm"
REQUIRED_ROOTS = {
    "TestGaugeReceiptBetweenTicks", "TestQueuedTickCannotRollReceiptClockBack",
    "TestGaugeFreshnessStillFailsClosed", "TestRetiredRemoteGaugeReadCannotReplaceSelectedHost",
    "TestUnrelatedUpdateDoesNotRefreshGaugeDeadlines", "TestReviewGaugeRetiredReceiptsCannotRenewHeldBudgets",
    "TestReviewGaugeClockCutPreservesOnlyCurrentRenderedClick", "TestIOChecklistMouseKeyboardAllNoneAndNoBackendMutations",
    "TestReviewGaugeCombinedMemoryUsageRemainsUnknown",
}
STEPS = ("toolchain", "go-environment", "node-version", "tui-race", "tui-vet", "native-build", "buildinfo", "web-tests", "gauge-pty", "dashboard-pty")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def source():
    git = lambda *args: subprocess.check_output(["git", *args], cwd=ROOT).decode().strip()
    files = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode().split("\0")
    return {"commit": git("rev-parse", "HEAD"), "tree": git("rev-parse", "HEAD^{tree}"),
            "status": git("status", "--porcelain"), "files": {name: sha(ROOT / name) for name in files if name}}


def main():
    # These checks precede all subprocess test/build execution. No local queue,
    # publication, credentialed mutation or user backend is a fallback.
    checks = {"hosted": os.environ.get("GITHUB_ACTIONS") == "true",
              "repository": os.environ.get("GITHUB_REPOSITORY") == REPOSITORY,
              "branch": os.environ.get("GITHUB_REF") == BRANCH,
              "event": os.environ.get("GITHUB_EVENT_NAME") in ("push", "workflow_dispatch"),
              "sha": re.fullmatch(r"[0-9a-f]{40}", os.environ.get("GITHUB_SHA", "")) is not None,
              "runner_label": os.environ.get("GAUGE_RUNNER_LABEL") in ("ubuntu-24.04", "macos-15"),
              "no_arm_overrides": not any(name.startswith("ARM_") for name in os.environ)}
    if not all(checks.values()):
        print(json.dumps({"refused_before_execution": checks}))
        return 2
    child_umask = os.umask(0o077)
    output = ROOT / ".cache/gauge-ci"
    output.mkdir(parents=True, exist_ok=False)
    work, receipts = output / "work", output / "receipts"
    work.mkdir(); receipts.mkdir()
    before = source()
    write(work / "source-before.json", before)
    if before["commit"] != os.environ["GITHUB_SHA"] or before["status"]:
        raise RuntimeError("dirty source or checkout differs from immutable hosted SHA")
    inputs = json.loads((ROOT / "scripts/gauge-source-inputs.json").read_text())
    actual_scope = {name for name in before["files"] if name.startswith("internal/tui/") and name.endswith(".go")}
    actual_scope |= {"scripts/test-terminal-dashboard.py", "scripts/test-terminal-gauge-refresh.py"}
    if {item["path"] for item in inputs["files"]} != actual_scope:
        raise RuntimeError("gauge equivalence roster omitted source")
    for item in inputs["files"]:
        new_consumer = item.get("new_owned_regression") and item["path"] == "internal/tui/memory_projection_review_test.go"
        fixture_oracle = item.get("fixture_oracle_adjustment") and item["path"] in {"internal/tui/integration_test.go", "internal/tui/lease_checkins_integration_test.go", "scripts/test-terminal-dashboard.py"}
        if not (item["normalized_equal"] or new_consumer or fixture_oracle) or before["files"][item["path"]] != item["canonical_sha256"]:
            raise RuntimeError("canonical gauge bytes differ from reviewed equivalence roster")
    dependencies = inputs["accepted_dependencies"]
    if set(dependencies["files"]) != {"internal/model/memory.go", "internal/observe/availability_linux.go", "internal/observe/availability_linux_test.go", "internal/observe/observe.go"}:
        raise RuntimeError("memory producer dependency closure incomplete")
    if any(before["files"][name] != value for name, value in dependencies["files"].items()):
        raise RuntimeError("memory producer differs from the accepted source")
    write(work / "source-equivalence.json", inputs)
    metadata = {name: os.environ.get(name) for name in ("GITHUB_SHA", "GITHUB_REF", "GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "RUNNER_OS", "RUNNER_ARCH", "GAUGE_RUNNER_LABEL", "ImageOS", "ImageVersion")}
    metadata["child_umask"] = oct(child_umask)
    metadata.update(platform=platform.platform(), machine=platform.machine(), python=sys.version, cpu_count=os.cpu_count(), macos_version=platform.mac_ver()[0])
    if sys.platform == "linux":
        release = dict(line.split("=", 1) for line in Path("/etc/os-release").read_text().splitlines() if "=" in line and not line.startswith("#"))
        metadata["linux_distribution"] = {key: release.get(key, "").strip('"') for key in ("NAME", "VERSION_ID", "PRETTY_NAME")}
    expected_platform = "darwin" if os.environ["GAUGE_RUNNER_LABEL"] == "macos-15" else "linux"
    if sys.platform != expected_platform:
        raise RuntimeError("hosted label does not match actual operating system")
    write(work / "metadata.json", metadata)
    commands, failures, image = [], [], None
    active = None

    def interrupted(signum, frame):
        raise KeyboardInterrupt("hosted validation interrupted by signal " + str(signum))

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)

    def stop(process):
        for sig, wait in ((signal.SIGINT, 20), (signal.SIGTERM, 5), (signal.SIGKILL, 5)):
            try:
                os.killpg(process.pid, sig)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=wait)
                return
            except subprocess.TimeoutExpired:
                continue
        process.wait()

    def run(name, argv, env, timeout=600):
        nonlocal active
        print("START", name, flush=True)
        started = time.monotonic()
        code, pid, control, waited = 127, None, 0, False
        with (work / (name + ".stdout.txt")).open("wb") as stdout, (work / (name + ".stderr.txt")).open("wb") as stderr:
            try:
                active = subprocess.Popen(argv, cwd=ROOT, env=env, stdout=stdout, stderr=stderr, start_new_session=True, umask=child_umask)
                pid = active.pid
                try:
                    code = active.wait(timeout=timeout)
                    waited = True
                except subprocess.TimeoutExpired:
                    stop(active)
                    code, control, waited = active.returncode, 124, True
                except BaseException:
                    stop(active)
                    code, control, waited = active.returncode, 130, True
                    raise
            except OSError as error:
                stderr.write(str(error).encode())
            finally:
                if active is not None and active.poll() is None:
                    stop(active)
                active = None
                commands.append(dict(name=name, argv=argv, pid=pid, exit=code, controller_exit=control, waited=waited, seconds=round(time.monotonic()-started, 6)))
                write(work / "commands.json", commands)
        if code or control or not waited:
            failures.append(name)
        print("END", name, "exit", code, flush=True)
        return control or code

    try:
        with tempfile.TemporaryDirectory(prefix="arm-gauge-hosted-", dir=os.environ["RUNNER_TEMP"]) as scratch:
            private = Path(scratch).resolve()
            for name in ("home", "tmp", "build", "modules", "gopath", "config", "data", "state"):
                (private / name).mkdir(mode=0o700)
            # No GitHub/runtime token, ARM, GOOS/GOARCH, GODEBUG, CGo override,
            # user agent config or private module credentials reach test children.
            env = {name: os.environ[name] for name in ("PATH", "LANG", "LC_ALL", "SYSTEMROOT", "DEVELOPER_DIR", "SDKROOT", "SSL_CERT_FILE", "SSL_CERT_DIR") if name in os.environ}
            env.update(HOME=str(private / "home"), TMPDIR=str(private / "tmp"), TMP=str(private / "tmp"), TEMP=str(private / "tmp"),
                       XDG_CONFIG_HOME=str(private / "config"), XDG_DATA_HOME=str(private / "data"), XDG_STATE_HOME=str(private / "state"),
                       GOCACHE=str(private / "build"), GOMODCACHE=str(private / "modules"), GOPATH=str(private / "gopath"),
                       GOMAXPROCS="1", GOFLAGS="-p=1", GOTOOLCHAIN="local", GOWORK="off", GOENV="off", GO111MODULE="on",
                       GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOPRIVATE="", GONOPROXY="", GONOSUMDB="", GOINSECURE="",
                       GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT="0")
            write(work / "environment.json", env)
            go, node = shutil.which("go", path=env["PATH"]), shutil.which("node", path=env["PATH"])
            if not go or not node:
                raise RuntimeError("native Go or Node executable unavailable")
            write(work / "tools.json", {"go": {"path": go, "sha256": sha(Path(go))}, "node": {"path": node, "sha256": sha(Path(node))}})
            run("toolchain", [go, "version"], env)
            if not run("go-environment", [go, "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOVERSION", "GOMOD", "GOROOT"], env):
                goenv = json.loads((work / "go-environment.stdout.txt").read_text())
                native_os = "darwin" if sys.platform == "darwin" else "linux"
                native_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
                expected_go = "go" + re.search(r"^go ([0-9.]+)$", (ROOT / "go.mod").read_text(), re.MULTILINE).group(1)
                if not (ROOT / "go.mod").read_text().startswith("module github.com/inferal-oss/arm\n") or goenv["GOVERSION"] != expected_go:
                    raise RuntimeError("module or exact Go toolchain differs from canonical source")
                if goenv["GOOS"] != native_os or goenv["GOARCH"] != native_arch or Path(goenv["GOMOD"]).resolve() != ROOT / "go.mod":
                    raise RuntimeError("toolchain is cross-targeted or uses another module")
                write(work / "native-target.json", dict(os=native_os, arch=native_arch, **goenv))
            run("node-version", [node, "--version"], env)
            run("tui-race", [go, "test", "-mod=readonly", "-race", "-p=1", "-count=1", "-json", "-timeout=10m", "./internal/tui"], env, 900)
            run("tui-vet", [go, "vet", "-mod=readonly", "-p=1", "./internal/tui"], env)
            binary = work / "arm"
            if not run("native-build", [go, "build", "-mod=readonly", "-p=1", "-o", str(binary), "./cmd/arm"], env):
                image = sha(binary)
                write(work / "image.json", dict(sha256=image, bytes=binary.stat().st_size))
                if not run("buildinfo", [go, "version", "-m", str(binary)], env):
                    info = (work / "buildinfo.stdout.txt").read_text()
                    if "vcs.revision=" + before["commit"] not in info or "vcs.modified=false" not in info or "github.com/inferal-oss/arm/cmd/arm" not in info:
                        failures.append("native image buildinfo differs from exact clean source")
            web = sorted(name for name in before["files"] if name.startswith("internal/server/web/") and name.endswith(".test.mjs"))
            if not web:
                raise RuntimeError("empty web test roster")
            run("web-tests", [node, "--test", *web], env)
            if image:
                run("gauge-pty", [sys.executable, "-B", "scripts/test-terminal-gauge-refresh.py", "--arm", str(binary), "--output-dir", str(work / "gauge-pty")], env, 180)
                run("dashboard-pty", [sys.executable, "-B", "scripts/test-terminal-dashboard.py", "--arm", str(binary), "--output-dir", str(work / "dashboard-pty")], env, 180)
            events = [json.loads(line) for line in (work / "tui-race.stdout.txt").read_text().splitlines() if line.startswith("{")]
            roster = {action: [e for e in events if e.get("Action") == action] for action in ("run", "pass", "fail", "skip", "build-fail")}
            write(work / "roster.json", roster)
            passed = {e.get("Test") for e in roster["pass"]}
            if not REQUIRED_ROOTS <= passed or roster["fail"] or roster["build-fail"]:
                failures.append("missing required TUI success or failed race event")
            for name in ("gauge-pty", "dashboard-pty"):
                if not any(c["name"] == name and c["exit"] == 0 and c["controller_exit"] == 0 and c["waited"] for c in commands):
                    failures.append("incomplete " + name)
            if image and sha(binary) != image:
                failures.append("native image changed during execution")
            after = source()
            write(work / "source-after.json", after)
            if before != after:
                failures.append("source changed during validation")
            write(work / "closure.json", dict(source_unchanged=before == after, image_unchanged=bool(image) and sha(binary) == image, all_commands_waited=bool(commands) and all(c["waited"] for c in commands)))
            binary.unlink(missing_ok=True)
        write(work / "cleanup.json", dict(private_root_removed=not Path(scratch).exists(), user_install_or_backend_accessed=False))
    except BaseException as error:
        failures.append(type(error).__name__ + ": " + str(error))
        if active is not None:
            stop(active)
    finally:
        if {c["name"] for c in commands} != set(STEPS):
            failures.append("incomplete command roster")
        write(work / "result.json", dict(failures=failures, source=before["commit"], complete=not failures))
        # Exact generated-file whitelist. Never upload the binary, caches,
        # backend SQLite state, copied source, ambient files or arbitrary trees.
        allowed = {"source-before.json", "source-after.json", "source-equivalence.json", "metadata.json", "commands.json", "environment.json", "native-target.json", "tools.json", "image.json", "closure.json", "cleanup.json", "roster.json", "result.json"}
        allowed |= {name + suffix for name in STEPS for suffix in (".stdout.txt", ".stderr.txt")}
        allowed |= {"gauge-pty/terminal.ansi", "gauge-pty/observations.json", "dashboard-pty/terminal.ansi", "dashboard-pty/messages.json"}
        manifest = {}
        for name in sorted(allowed):
            path = work / name
            if not path.exists():
                continue
            if path.is_symlink() or not path.is_file() or path.stat().st_size > 32 * 1024 * 1024 or work.resolve() not in path.resolve().parents:
                raise RuntimeError("unsafe or oversized receipt: " + name)
            target = receipts / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
            manifest[name] = dict(bytes=target.stat().st_size, sha256=sha(target))
        write(receipts / "manifest.json", manifest)
    print(json.dumps({"source": before["commit"], "failures": failures, "complete": not failures}), flush=True)
    return bool(failures)


if __name__ == "__main__":
    raise SystemExit(main())
