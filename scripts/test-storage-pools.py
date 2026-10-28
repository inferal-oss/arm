#!/usr/bin/env python3
"""Opt-in native storage-pool validation; raw output belongs outside version control.

Run with an exact candidate binary and explicitly approved existing directories.
Uses a fresh private authority, sequential admitted benchmarks, retained sample
conditions and ordinary cleanup. It never edits the user's normal ARM config.
"""
import argparse
import json
import pathlib
import statistics
import subprocess
import tempfile
import time


class CommandError(RuntimeError):
    def __init__(self, message, code=None):
        super().__init__(message)
        self.code = code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arm", required=True, type=pathlib.Path)
    parser.add_argument("--path", required=True, action="append")
    parser.add_argument("--samples", type=int, default=3, choices=range(1, 6))
    parser.add_argument("--size", default="256MiB")
    parser.add_argument("--distinct-backing", action="store_true")
    parser.add_argument("--handoff-owned-scratch", action="store_true",
                        help="explicitly assume residual physical storage responsibility after exact owned scratch namespace removal; no exclusive reclaim credit")
    args = parser.parse_args()
    binary = args.arm.expanduser().resolve(strict=True)
    paths = [pathlib.Path(p).expanduser().resolve(strict=True) for p in args.path]
    if not all(p.is_dir() for p in paths):
        parser.error("each --path must be an existing directory")
    home = pathlib.Path(tempfile.mkdtemp(prefix="arm-storage-validation-")).resolve()
    started = False
    records = []
    report = {"home": str(home), "paths": list(map(str, paths)), "samples": args.samples}

    def call(*command):
        child = subprocess.run([str(binary), "--home", str(home), *command, "--json"],
                               capture_output=True, text=True, timeout=100)
        if child.returncode:
            try:
                code = json.loads(child.stderr).get("error", {}).get("code")
            except (ValueError, AttributeError):
                code = None
            raise CommandError(f"{command[0]} failed ({child.returncode}): {child.stderr}", code)
        return json.loads(child.stdout)

    def settle_sample(sample):
        lease_id = sample["storage_lease"]
        deadline = time.monotonic() + 20
        while True:
            lease = next(l for l in call("storage", "list") if l["id"] == lease_id)
            if lease["state"] == "terminal":
                return
            if lease["state"] == "needs_attention":
                artifact = lease.get("artifact") or {}
                request = lease.get("request") or {}
                if not args.handoff_owned_scratch:
                    return  # retained obligation is checked by the final roster
                if not (artifact.get("namespace_removed") and artifact.get("content_observed")
                        and request.get("agent") == "storage-performance-validation"
                        and request.get("reason") == "Authorized bounded native storage comparison"):
                    raise RuntimeError("scratch cleanup uncertainty exceeds explicit removed-namespace handoff")
                try:
                    decision = call("storage", "handoff", lease_id)
                    report.setdefault("owned_scratch_handoffs", []).append(decision)
                    return
                except CommandError as error:
                    if error.code != "operation_busy":
                        raise
            if time.monotonic() >= deadline:
                raise RuntimeError("original scratch cleanup did not settle; obligations retained")
            time.sleep(.05)

    try:
        for i, path in enumerate(paths):
            call("config", "pool", "set", f"location-{i}", "--path", str(path),
                 "--tier", "validation", "--ceiling", "2GiB")
        call("config", "check")
        call("start", "--cpu", "1", "--memory", "128MiB")
        started = True
        report["agent"] = call("agent", "register", "--id", "storage-performance-validation",
                               "--adapter", "generic", "--delivery", "poll", "--cwd", str(pathlib.Path.cwd()))
        inventory = call("storage", "pools")
        report["inventory"] = inventory
        backing = [p["location"]["backing_pool"] for p in inventory]
        if not all(backing):
            raise RuntimeError("unknown backing topology; no native performance acceptance")
        if args.distinct_backing and len(set(backing)) != len(paths):
            raise RuntimeError("selected locations share backing capacity")
        for i, path in enumerate(paths):
            samples = []
            for _ in range(args.samples):
                samples.append(call("storage", "benchmark", f"location-{i}", "--size", args.size,
                                    "--reason", "Authorized bounded native storage comparison",
                                    "--agent", "storage-performance-validation"))
                settle_sample(samples[-1])
            records.append({"name": f"location-{i}", "path": str(path), "samples": samples,
                            "median_write_mib_s": statistics.median(
                                s["write_bytes_per_second"] / (1 << 20) for s in samples),
                            "median_read_mib_s": statistics.median(
                                s["read_bytes_per_second"] / (1 << 20) for s in samples)})
        report["measurements"] = records
        report["pool_views"] = call("storage", "pools")
        resources = call("resources")
        named = [r for r in resources if r.get("storage")]
        if len(named) != len(paths) or not all(r["storage"].get("performance") for r in named):
            raise RuntimeError("resource views lack the actual saved performance profiles")
        report["resources"] = named
        deadline = time.monotonic() + 20
        while True:
            leases = call("storage", "list")
            if all(l["state"] == "terminal" for l in leases):
                break
            if time.monotonic() >= deadline:
                report["unsettled"] = leases
                raise RuntimeError("native cleanup remains uncertain; obligations retained")
            time.sleep(.1)
        report["storage_leases"] = leases
        report["settlement_policy"] = ("Explicit operator handoff after owned scratch namespace removal; no exclusive block reclamation credit"
                                       if args.handoff_owned_scratch else "Ordinary conservative storage cleanup")
        report["outcome"] = "passed"
    except Exception as error:
        report["outcome"] = "failed"
        report["error"] = str(error)
        raise
    finally:
        if started:
            # Stopping the exact private authority never abandons its ledger.
            try:
                report["stop"] = call("stop")
            except Exception as error:
                report["stop_error"] = str(error)
                report["outcome"] = "failed"
        print(json.dumps(report, indent=2))
    if report["outcome"] != "passed":
        raise RuntimeError("private authority stop did not complete")


if __name__ == "__main__":
    main()
