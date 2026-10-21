#!/usr/bin/env python3
"""Run CGo-free test executables on an actual Linux kernel, using a cached image.

The image must contain /bin/sh and python3. No source tree, credentials, Docker
socket, or writable host directory is mounted into the disposable container.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", required=True, help="existing local Linux image with /bin/sh and python3")
    parser.add_argument("--go", default=os.environ.get("GO", "go"))
    parser.add_argument("--docker", default="docker")
    opts = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    image = json.loads(subprocess.check_output([opts.docker, "image", "inspect", opts.image], text=True))[0]
    if image["Os"] != "linux" or image["Architecture"] not in ("arm64", "amd64"):
        parser.error("image must be Linux arm64 or amd64")
    env = dict(os.environ, GOOS="linux", GOARCH=image["Architecture"], CGO_ENABLED="0")
    env.setdefault("GOCACHE", str(root / ".cache" / "go-build"))
    env.setdefault("GOPATH", str(root / ".cache" / "gopath"))
    with tempfile.TemporaryDirectory(prefix="arm-linux-tests-") as staging:
        target = Path(staging)
        for package in ("agents", "local", "install", "observe", "storage", "manager", "server", "cli"):
            subprocess.run([opts.go, "test", "-c", "-o", str(target / (package + ".test")), "./internal/" + package], cwd=root, env=env, check=True)
        subprocess.run([opts.go, "build", "-o", str(target / "arm"), "./cmd/arm"], cwd=root, env=env, check=True)
        shutil.copyfile(root / "scripts" / "test-local.py", target / "test-local.py")
        os.chmod(target, 0o755)
        for child in target.iterdir():
            child.chmod(0o755 if child.suffix != ".py" else 0o644)
        script = '''set -eu
uname -srmo
for suite in /tests/*.test; do
  "$suite" -test.v -test.timeout=60s
done
/tests/arm version
python3 /tests/test-local.py --arm /tests/arm
'''
        subprocess.run([
            opts.docker, "run", "--rm", "--pull", "never", "--init", "--network", "none",
            "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--pids-limit", "256", "--memory", "1g", "--cpus", "2", "--user", "65534:65534",
            "--tmpfs", "/tmp:rw,exec,nosuid,size=512m", "--env", "HOME=/tmp",
            "--mount", "type=bind,source=" + staging + ",target=/tests,readonly",
            "--entrypoint", "/bin/sh", image["Id"], "-c", script,
        ], check=True, timeout=240)


if __name__ == "__main__":
    main()
