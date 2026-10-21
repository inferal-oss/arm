#!/usr/bin/env python3
"""Exercise background daemon lifecycle and cancellation through the built CLI."""
import argparse
from contextlib import contextmanager
import fcntl
import json
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time


@contextmanager
def fixture_directory():
    root=tempfile.mkdtemp(prefix="arm-cli-")
    try:
        yield root
    finally:
        # Never remove an authority's state solely because an assertion failed.
        # Verify shutdown by taking its authority lock; an ambiguous fixture is
        # retained for recovery instead of becoming a running orphan with no DB.
        try:
            with (Path(root)/"state"/"daemon.lock").open("r+") as lock:
                fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                if (Path(root)/"state"/"endpoint.json").exists():
                    print("Retained test state with an unresolved endpoint: "+root,file=sys.stderr)
                else:
                    shutil.rmtree(root)
        except (FileNotFoundError,BlockingIOError):
            print("Retained test state because authority shutdown was not verified: "+root,file=sys.stderr)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--arm",default="bin/arm")
    opts=parser.parse_args()
    binary=str(Path(opts.arm).resolve())
    with fixture_directory() as root:
        home=str(Path(root)/"state")
        waiting=None
        def arm(*args):
            p=subprocess.run([binary,"--home",home,*args],capture_output=True,text=True,timeout=15)
            assert p.returncode==0,(args,p.returncode,p.stderr)
            return json.loads(p.stdout)
        try:
            arm("daemon","start","--cpu","1","--memory","1GiB","--disk","2GiB")
            retained=arm("storage","create","--disk","1MiB","--ttl","2m","--reason","retain the smoke-test output")
            assert retained["state"]=="active" and retained["artifact"]["identity"]
            arm("run","--storage",retained["id"],"--ttl","30s","--reason","write a retained result","--json","--","/bin/sh","-c",'printf result > "$ARM_STORAGE_DIR/result"')
            retained_file=Path(retained["managed_dir"])/"result"
            assert retained_file.read_bytes()==b"result"
            preview=arm("storage","cleanup",retained["id"],"--dry-run")
            assert preview["can_clean"] and preview["reclaimable_bytes"] is None,preview
            assert retained_file.exists(),"cleanup preview changed data"
            blocked=subprocess.run([binary,"--home",home,"uninstall","--dry-run"],capture_output=True,text=True,timeout=15)
            assert blocked.returncode!=0 and "active_leases" in blocked.stderr,"uninstall ignored retained storage"
            handed=arm("storage","handoff",retained["id"])
            assert handed["outcome"]=="handed_off" and retained_file.exists()
            arm("storage","cleanup",retained["id"])
            assert retained_file.exists(),"cleanup removed handed-off user data"
            cleanup=arm("storage","create","--disk","1MiB","--ttl","2m","--reason","test verified cleanup accounting")
            (Path(cleanup["managed_dir"])/"payload").write_bytes(b"unknown exclusive blocks")
            arm("storage","cleanup",cleanup["id"])
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                state=arm("inspect",cleanup["id"])
                if state["state"]=="needs_attention": break
                time.sleep(.05)
            else: raise AssertionError("storage cleanup did not resolve its operation")
            assert state["artifact"]["namespace_removed"] and state["artifact"]["last_error"]
            assert arm("status")["reserved"]["disk_bytes"]==1<<20,"unverified reclamation was declared free"
            arm("storage","handoff",cleanup["id"])
            occupied=arm("acquire","--cpu","1","--reason","occupy the test budget","--ttl","1m")
            resources=arm("resources")
            assert resources[0]["available"]==0
            assert resources[0]["users"][0]["reason"]=="occupy the test budget"
            assert resources[2]["measurement"]["total"]["value"]>0
            scan_root=Path(root)/"storage"
            scan_root.mkdir()
            (scan_root/"payload").write_bytes(b"arm")
            measured=arm("storage","inspect",str(scan_root))
            assert measured["logical"]["value"]==3 and measured["logical"]["complete"]
            assert measured["exclusive"]["value"] is None and measured["reclaimable"]["value"] is None
            marker=Path(root)/"must-not-start"
            waiting=subprocess.Popen([binary,"--home",home,"run","--wait","--reason","cancel this queued command","--","/bin/sh","-c",'echo unexpected > "$1"',"sh",str(marker)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                matches=[l for l in arm("status")["leases"] if l["request"]["reason"]=="cancel this queued command"]
                if matches and matches[0]["state"]=="queued": break
                time.sleep(.05)
            else: raise AssertionError("command was not queued")
            waiting.send_signal(signal.SIGINT)
            waiting.communicate(timeout=10)
            assert waiting.returncode==130,waiting.returncode
            cancelled=arm("inspect",matches[0]["id"])
            assert cancelled["state"]=="terminal" and cancelled["outcome"]=="cancelled",cancelled
            arm("release",occupied["id"])
            time.sleep(.25)
            assert not marker.exists(),"cancelled command started after capacity was released"
            assert arm("daemon","stop")["stopped"]
            print("Local CLI: daemon lifecycle, storage retention/preview/handoff, uninstall guard, conservative cleanup, who/why budgets, filesystem measurements, and queued Ctrl-C cancellation passed")
        finally:
            if waiting and waiting.poll() is None:
                waiting.send_signal(signal.SIGINT)
                try: waiting.wait(timeout=5)
                except subprocess.TimeoutExpired: waiting.kill();waiting.wait()
            # A failed assertion must not leave a background authority running
            # after TemporaryDirectory removes its state. Cancel only this
            # fixture's recorded execution leases, then explicitly hand off its
            # storage obligations before stopping the private backend.
            try:
                status=arm("status")
                for lease in status["leases"]:
                    if lease["state"]!="terminal" and lease["request"].get("kind")!="storage":
                        subprocess.run([binary,"--home",home,"cancel",lease["id"]],capture_output=True,timeout=15)
                deadline=time.monotonic()+40
                while time.monotonic()<deadline:
                    leases=arm("status")["leases"]
                    outstanding=[lease for lease in leases if lease["state"]!="terminal"]
                    if not outstanding: break
                    for lease in outstanding:
                        if lease["request"].get("kind")=="storage":
                            subprocess.run([binary,"--home",home,"storage","handoff",lease["id"]],capture_output=True,timeout=15)
                        elif lease["state"]=="needs_attention":
                            subprocess.run([binary,"--home",home,"reconcile",lease["id"]],capture_output=True,timeout=15)
                    time.sleep(.05)
            except (AssertionError,subprocess.TimeoutExpired):
                pass  # A normally stopped backend has no discoverable endpoint.
            subprocess.run([binary,"--home",home,"daemon","stop"],capture_output=True,timeout=10)


if __name__=="__main__":
    main()
