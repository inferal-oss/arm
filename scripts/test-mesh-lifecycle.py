#!/usr/bin/env python3
"""Actual native-to-Linux automatic mesh lifecycle acceptance.

Requires a trusted direct SSH destination and an already installed compatible
Linux ARM executable. All backend state and test helper copies are disposable.
No default ARM home, agent files, SSH trust files or global packages are changed.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import socket
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arm", required=True)
    parser.add_argument("--ssh", required=True)
    parser.add_argument("--remote-binary", required=True)
    parser.add_argument("--remote-root", required=True, help="existing private disposable /tmp directory")
    args = parser.parse_args()
    assert args.remote_root.startswith("/tmp/") and ".." not in Path(args.remote_root).parts and re.fullmatch(r"/[A-Za-z0-9_./-]+", args.remote_root)
    assert re.fullmatch(r"/[A-Za-z0-9_./-]+", args.remote_binary)
    assert not args.ssh.startswith("-")
    arm = str(Path(args.arm).resolve())
    local_root = Path(tempfile.mkdtemp(prefix="arm-mesh-lifecycle-"))
    local_home = local_root / "state"
    remote_root = args.remote_root + "/lifecycle-" + uuid.uuid4().hex
    remote_home, remote_arm = remote_root + "/state", remote_root + "/arm"

    def ssh(command, check=True):
        p = subprocess.run(["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no", "--", args.ssh, command], text=True, capture_output=True, timeout=30)
        if check:
            assert p.returncode == 0, p.stderr
        return p

    def local(command, ok=True):
        p = subprocess.run([arm, "--home", str(local_home), *command], text=True, capture_output=True, timeout=60)
        if ok:
            assert p.returncode == 0, (command, p.stderr)
            return json.loads(p.stdout)
        assert p.returncode != 0, command
        return p

    def remote(command, ok=True):
        p = ssh(shlex.join([remote_arm, "--home", remote_home, *command]), check=ok)
        if ok:
            return json.loads(p.stdout)
        assert p.returncode != 0, command
        return p

    def connect(key, extra=()):
        return ["mesh", "connect", args.ssh, "--binary", remote_arm, "--remote-home", remote_home, "--key", key, *extra]

    def wait_stopped():
        for _ in range(50):
            if not (local_home / "endpoint.json").exists() and ssh("test ! -e " + shlex.quote(remote_home + "/endpoint.json"), check=False).returncode == 0:
                return
            time.sleep(.1)
        raise AssertionError("fixture backends did not stop")

    def closed(address):
        host, port = address.rsplit(":", 1)
        host = host.strip("[]")
        try:
            with socket.create_connection((host, int(port)), timeout=1):
                raise AssertionError("disconnected listener still accepts TCP")
        except OSError:
            pass

    ssh("umask 077; mkdir " + shlex.quote(remote_root) + " && cp " + shlex.quote(args.remote_binary) + " " + shlex.quote(remote_arm) + " && chmod 700 " + shlex.quote(remote_arm))
    success = False
    try:
        # Both homes absent: no-state preview and missing machine confirmation.
        local(connect("readonly") + ["--dry-run"])
        assert not local_home.exists()
        assert ssh("test ! -e " + shlex.quote(remote_home), check=False).returncode == 0
        local(connect("readonly"), ok=False)
        assert not local_home.exists()
        assert ssh("test ! -e " + shlex.quote(remote_home), check=False).returncode == 0

        result = local(connect("automatic") + ["--yes"])
        target = result["connected"]
        own = result["local_identity"]["host"]
        source_address = result["local_identity"]["address"]
        target_address = result["remote_identity"]["address"]
        assert result["source_permissions_on_target"]["permissions"] == ["observe-host"]
        assert result["target_permissions_on_source"]["permissions"] == []
        local(["status", "--host", target])
        local(["acquire", "--host", target, "--cpu", ".125", "--ttl", "30s", "--reason", "must be denied", "--key", "denied"], ok=False)
        remote(["status", "--host", own], ok=False)
        local(connect("automatic") + ["--yes"])

        # Persisted settings/endpoint/pins survive both stopped authorities.
        startup = json.loads((local_home / "daemon-launch.json").read_text())
        local(["stop"])
        remote(["stop"])
        wait_stopped()
        resumed = local(connect("automatic") + ["--yes"])
        assert resumed["local_identity"] == result["local_identity"]
        assert resumed["remote_identity"] == result["remote_identity"]
        assert json.loads((local_home / "daemon-launch.json").read_text())["resources"] == startup["resources"]

        # Model a crash AFTER the native connected journal, BEFORE its durable
        # coordinator outcome: this exact private receipt is fault injection.
        # Recovery must add its own concrete approved review and real mTLS hello,
        # while preserving the original review and changing no trust history.
        controls = local_home / "mesh-lifecycle"
        receipt_path = controls / ("coordinator-operation-" + hashlib.sha256(b"automatic").hexdigest() + ".json")
        receipt = json.loads(receipt_path.read_text())
        receipt["phase"] = "incomplete"
        receipt.pop("result", None)
        receipt_path.write_text(json.dumps(receipt) + "\n")
        old_keys = {p.name for p in controls.glob("coordinator-operation-*.json")}
        trust_before = json.loads((local_home / "mesh" / "config.json").read_text())["history"]
        recovered = local(connect("automatic") + ["--yes"])
        assert recovered["reused"] is True and recovered["connected"] == target
        new_paths = [p for p in controls.glob("coordinator-operation-*.json") if p.name not in old_keys]
        assert len(new_paths) == 1
        child = json.loads(new_paths[0].read_text())
        assert child["phase"] == "completed" and child["approved"] is True
        assert child["review"]["operation"] == "verify-existing-connection"
        assert child["review"]["pair"] == result["pair"]
        after_receipt = json.loads(receipt_path.read_text())
        assert after_receipt["request"] == receipt["request"] and after_receipt["review"] == receipt["review"] and after_receipt["actor"] == receipt["actor"]
        assert json.loads((local_home / "mesh" / "config.json").read_text())["history"] == trust_before

        # A failed target helper leaves durable local denial and exact retry.
        ssh("mv " + shlex.quote(remote_arm) + " " + shlex.quote(remote_arm + ".offline"))
        disconnected = local(["mesh", "disconnect", target, "--key", "disconnect", "--yes"])
        assert disconnected["remote_pending"] is True
        local(["status", "--host", target], ok=False)
        closed(source_address)
        ssh("mv " + shlex.quote(remote_arm + ".offline") + " " + shlex.quote(remote_arm))
        assert local(["mesh", "disconnect", target, "--key", "disconnect", "--yes"])["remote_pending"] is False
        closed(target_address)

        # Explicit re-pair adds only the reviewed lease grant. Disconnect never
        # silently ends resource obligations on the destination authority.
        extra = ["--replace-revoked", "--grant", "observe-host,observe-own,lease-own", "--cpu", "1", "--memory", "128MiB", "--disk", "128MiB", "--max-ttl", "1m", "--yes"]
        repaired = local(connect("repair", extra))
        local(["mesh", "disconnect", target, "--key", "disconnect", "--yes"], ok=False)
        local(["status", "--host", target])
        lease = local(["acquire", "--host", target, "--cpu", ".125", "--memory", "1MiB", "--ttl", "45s", "--reason", "automatic mesh acceptance", "--key", "owned-lease"])
        local(["mesh", "disconnect", target, "--key", "disconnect-with-obligation", "--yes"])
        retained = remote(["inspect", lease["id"]])
        assert retained["state"] == "active", retained
        remote(["release", lease["id"]])
        assert repaired["local_identity"]["fingerprint"] == result["local_identity"]["fingerprint"]
        assert repaired["remote_identity"]["fingerprint"] == result["remote_identity"]["fingerprint"]
        success = True
        print("PASS automatic two-host connect: no-state preview, default grants, exact retry, both-stopped restart, reviewed connected-outcome recovery with fresh mTLS and immutable original receipt, pin/endpoint/budget preservation, local-first offline disconnect/resume, reviewed re-pair, old-disconnect protection and retained lease obligations")
    finally:
        # Only these exact private fixture homes are stopped. Keep failure
        # evidence but do not strand backend/listener processes.
        if ssh("test -e " + shlex.quote(remote_arm + ".offline"), check=False).returncode == 0:
            ssh("mv " + shlex.quote(remote_arm + ".offline") + " " + shlex.quote(remote_arm), check=False)
        subprocess.run([arm, "--home", str(local_home), "stop", "--force"], capture_output=True, timeout=20)
        ssh(shlex.join([remote_arm, "--home", remote_home, "stop", "--force"]), check=False)
        if success:
            import shutil
            shutil.rmtree(local_root)
            ssh("rm -rf -- " + shlex.quote(remote_root))
        else:
            print("FAIL fixture evidence retained in disposable native/remote test roots; backend stop attempted", flush=True)
            print("Native evidence:", local_root, flush=True)


if __name__ == "__main__":
    main()
