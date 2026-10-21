#!/usr/bin/env python3
"""Run compiled CGo-free acceptance tests on an explicitly selected Linux host.

Only test executables, the ARM binary, and smoke-test code are uploaded. No
credentials, source checkout, agent sessions or global instruction files change.
An unsuccessful remote fixture is retained for inspection rather than deleted.
"""
import argparse
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", required=True, help="explicit SSH destination, e.g. user@linux-host")
    parser.add_argument("--go", default=os.environ.get("GO", "go"))
    parser.add_argument("--packages", nargs="+", choices=("agents", "local", "install", "observe", "usage", "storage", "ports", "mesh", "manager", "server", "client", "tui", "cli"), help="selected suites (default: all)")
    parser.add_argument("--test-run", default="", help="Go test regular expression for a targeted verification")
    parser.add_argument("--skip-smoke", action="store_true", help="skip compiled CLI smoke test for a targeted verification")
    parser.add_argument("--isolate-processes", action="store_true", help="run under unprivileged unshare with a private /proc; separately test host permission restrictions")
    opts = parser.parse_args()
    if opts.host.startswith("-"):
        parser.error("host must be an SSH destination")
    ssh = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", opts.host]
    machine = subprocess.check_output(ssh + ["uname -s; uname -m"], text=True).splitlines()
    if len(machine) != 2 or machine[0] != "Linux" or machine[1] not in ("aarch64", "x86_64"):
        parser.error("target must be Linux arm64 or amd64")
    root = Path(__file__).resolve().parents[1]
    env = dict(os.environ, GOOS="linux", GOARCH={"aarch64": "arm64", "x86_64": "amd64"}[machine[1]], CGO_ENABLED="0")
    env.setdefault("GOCACHE", str(root / ".cache" / "go-build"))
    env.setdefault("GOPATH", str(root / ".cache" / "gopath"))
    with tempfile.TemporaryDirectory(prefix="arm-linux-ssh-") as staging:
        target = Path(staging)
        for package in opts.packages or ("agents", "local", "install", "observe", "usage", "storage", "ports", "mesh", "manager", "server", "client", "tui", "cli"):
            subprocess.run([opts.go, "test", "-c", "-o", str(target / (package + ".test")), "./internal/" + package], cwd=root, env=env, check=True)
        subprocess.run([opts.go, "build", "-o", str(target / "arm"), "./cmd/arm"], cwd=root, env=env, check=True)
        shutil.copyfile(root / "scripts" / "linux-test-init.py", target / "linux-test-init.py")
        shutil.copyfile(root / "scripts" / "test-local.py", target / "test-local.py")
        shutil.copyfile(root / "scripts" / "test-ports.py", target / "test-ports.py")
        shutil.copyfile(root / "scripts" / "test-inbox.py", target / "test-inbox.py")
        shutil.copyfile(root / "scripts" / "test-pools.py", target / "test-pools.py")
        shutil.copyfile(root / "scripts" / "test-usage.py", target / "test-usage.py")
        shutil.copyfile(root / "scripts" / "test-policy.py", target / "test-policy.py")
        shutil.copyfile(root / "scripts" / "test-terminal.py", target / "test-terminal.py")
        shutil.copyfile(root / "scripts" / "test-monitor.py", target / "test-monitor.py")
        shutil.copyfile(root / "scripts" / "test-dashboard.py", target / "test-dashboard.py")
        shutil.copyfile(root / "scripts" / "test-terminal-dashboard.py", target / "test-terminal-dashboard.py")
        shutil.copyfile(root / "scripts" / "test-memory.py", target / "test-memory.py")
        shutil.copyfile(root / "scripts" / "test-lifecycle.py", target / "test-lifecycle.py")
        script = '''#!/bin/sh
set -eu
uname -srmo
id
for suite in ./*.test; do
  "$(pwd)/${suite#./}" -test.v -test.timeout=90s TEST_SELECTION
done
./arm version
'''.replace("TEST_SELECTION", "-test.run=" + shlex.quote(opts.test_run) if opts.test_run else "")
        # Opt-in strict native collection must reach the remote test process;
        # setting it only for the local cross-compiler silently skips that gate.
        if os.environ.get("ARM_TEST_NATIVE_MONITOR") == "1":
            script = script.replace("set -eu\n", "set -eu\nexport ARM_TEST_NATIVE_MONITOR=1\n", 1)
        if not opts.skip_smoke:
            script += 'python3 ./test-local.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-ports.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-inbox.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-pools.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-usage.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-policy.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-terminal.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-monitor.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-dashboard.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-terminal-dashboard.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-memory.py --arm "$PWD/arm"\n'
            script += 'python3 ./test-lifecycle.py --arm "$PWD/arm"\n'
        (target / "run.sh").write_text(script)
        remote = subprocess.check_output(ssh + ["mktemp -d /tmp/arm-tests.XXXXXXXXXX"], text=True).strip()
        if not re.fullmatch(r"/tmp/arm-tests\.[A-Za-z0-9]{10}", remote):
            raise RuntimeError("unexpected remote fixture path; no cleanup attempted")
        succeeded = False
        try:
            subprocess.run(["scp", "-q", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", *map(str, target.iterdir()), opts.host + ":" + remote + "/"], check=True)
            runner = "/bin/sh ./run.sh"
            if opts.isolate_processes:
                runner = "unshare --user --map-current-user --pid --fork --kill-child --mount-proc python3 ./linux-test-init.py " + runner
            subprocess.run(ssh + ["cd " + shlex.quote(remote) + " && timeout --signal=TERM --kill-after=10s 240s " + runner], check=True, timeout=270)
            succeeded = True
        finally:
            if succeeded:
                subprocess.run(ssh + ["rm -r -- " + shlex.quote(remote)], check=True)
            else:
                print("Remote test fixture retained:", opts.host + ":" + remote, flush=True)


if __name__ == "__main__":
    main()
