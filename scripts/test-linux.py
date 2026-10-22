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
        for package in ("agents", "model", "database", "local", "install", "observe", "usage", "storage", "sharefs", "ports", "mesh", "meshssh", "meshops", "meshstream", "sshservice", "cargoprofile", "workspace", "workspacetransport", "manager", "server", "client", "tui", "cli"):
            subprocess.run([opts.go, "test", "-c", "-o", str(target / (package + ".test")), "./internal/" + package], cwd=root, env=env, check=True)
        subprocess.run([opts.go, "build", "-o", str(target / "arm"), "./cmd/arm"], cwd=root, env=env, check=True)
        subprocess.run([opts.go, "build", "-o", str(target / "node-registry-probe"), "./scripts/node-registry-probe"], cwd=root, env=env, check=True)
        shutil.copyfile(root / "scripts" / "test-local.py", target / "test-local.py")
        shutil.copyfile(root / "scripts" / "test-ports.py", target / "test-ports.py")
        shutil.copyfile(root / "scripts" / "test-inbox.py", target / "test-inbox.py")
        shutil.copyfile(root / "scripts" / "test-pools.py", target / "test-pools.py")
        shutil.copyfile(root / "scripts" / "test-subpools.py", target / "test-subpools.py")
        shutil.copyfile(root / "scripts" / "test-usage.py", target / "test-usage.py")
        shutil.copyfile(root / "scripts" / "test-policy.py", target / "test-policy.py")
        shutil.copyfile(root / "scripts" / "test-terminal.py", target / "test-terminal.py")
        shutil.copyfile(root / "scripts" / "test-monitor.py", target / "test-monitor.py")
        shutil.copyfile(root / "scripts" / "test-dashboard.py", target / "test-dashboard.py")
        shutil.copyfile(root / "scripts" / "test-terminal-dashboard.py", target / "test-terminal-dashboard.py")
        shutil.copyfile(root / "scripts" / "test-terminal-colors.py", target / "test-terminal-colors.py")
        shutil.copyfile(root / "scripts" / "test-terminal-pools.py", target / "test-terminal-pools.py")
        shutil.copyfile(root / "scripts" / "test-terminal-recovery.py", target / "test-terminal-recovery.py")
        shutil.copyfile(root / "scripts" / "test-terminal-mesh.py", target / "test-terminal-mesh.py")
        shutil.copyfile(root / "scripts" / "test-memory.py", target / "test-memory.py")
        shutil.copyfile(root / "scripts" / "test-live-disk.py", target / "test-live-disk.py")
        shutil.copyfile(root / "scripts" / "test-database.py", target / "test-database.py")
        shutil.copyfile(root / "scripts" / "test-database-reset.py", target / "test-database-reset.py")
        shutil.copyfile(root / "scripts" / "test-node-metadata.py", target / "test-node-metadata.py")
        shutil.copyfile(root / "scripts" / "test-node-registry.py", target / "test-node-registry.py")
        shutil.copyfile(root / "scripts" / "test-lifecycle.py", target / "test-lifecycle.py")
        shutil.copyfile(root / "scripts" / "test-scheduling.py", target / "test-scheduling.py")
        shutil.copyfile(root / "scripts" / "test-budgets.py", target / "test-budgets.py")
        shutil.copyfile(root / "scripts" / "test-workspace-lifecycle.py", target / "test-workspace-lifecycle.py")
        shutil.copyfile(root / "scripts" / "test-workspaces.py", target / "test-workspaces.py")
        shutil.copyfile(root / "scripts" / "test-mesh.py", target / "test-mesh.py")
        shutil.copyfile(root / "scripts" / "test-agent-messages.py", target / "test-agent-messages.py")
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
python3 /tests/test-ports.py --arm /tests/arm
python3 /tests/test-inbox.py --arm /tests/arm
python3 /tests/test-pools.py --arm /tests/arm
python3 /tests/test-subpools.py --arm /tests/arm
python3 /tests/test-usage.py --arm /tests/arm
python3 /tests/test-policy.py --arm /tests/arm
python3 /tests/test-terminal.py --arm /tests/arm
python3 /tests/test-monitor.py --arm /tests/arm
python3 /tests/test-dashboard.py --arm /tests/arm
python3 /tests/test-terminal-dashboard.py --arm /tests/arm
python3 /tests/test-terminal-colors.py --arm /tests/arm
python3 /tests/test-terminal-pools.py --arm /tests/arm
python3 /tests/test-terminal-recovery.py --arm /tests/arm
python3 /tests/test-terminal-mesh.py --arm /tests/arm
python3 /tests/test-memory.py --arm /tests/arm
python3 /tests/test-live-disk.py --arm /tests/arm
python3 /tests/test-database.py --arm /tests/arm
python3 /tests/test-database-reset.py --arm /tests/arm
python3 /tests/test-node-metadata.py --arm /tests/arm
python3 /tests/test-node-registry.py --arm /tests/arm --probe /tests/node-registry-probe
python3 /tests/test-lifecycle.py --arm /tests/arm
python3 /tests/test-scheduling.py --arm /tests/arm
python3 /tests/test-budgets.py --arm /tests/arm
python3 /tests/test-workspace-lifecycle.py --arm /tests/arm --manager-tests /tests/manager.test
python3 /tests/test-workspaces.py --arm /tests/arm
python3 /tests/test-agent-messages.py --arm /tests/arm
'''
        subprocess.run([
            opts.docker, "run", "--rm", "--pull", "never", "--init", "--network", "none",
            "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--pids-limit", "256", "--memory", "1g", "--cpus", "2", "--user", "65534:65534",
            "--tmpfs", "/tmp:rw,exec,nosuid,size=512m", "--env", "HOME=/tmp",
            *(["--env", "ARM_TEST_NATIVE_MONITOR=1"] if os.environ.get("ARM_TEST_NATIVE_MONITOR") == "1" else []),
            "--mount", "type=bind,source=" + staging + ",target=/tests,readonly",
            "--entrypoint", "/bin/sh", image["Id"], "-c", script,
        ], check=True, timeout=240)


if __name__ == "__main__":
    main()
