#!/usr/bin/env python3
"""Build four real carriers from the exact checkout; never installs a user binary."""
import argparse
import gzip
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tarfile
import tempfile

from common import PLATFORMS, digest, filenames, verify_directory, version

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from owned_process import run_owned_command

PROCESS_STOP_TIMEOUT = 5


def interrupted(_signum, _frame):
    raise RuntimeError("release_build_interrupted")


def run(args, *, env=None, timeout=1800):
    result = run_owned_command(args, env=env, timeout=timeout,
                               capture_stderr=False, stop_timeout=PROCESS_STOP_TIMEOUT)
    if result.returncode:
        raise RuntimeError("release command failed: " + args[0])
    return result.stdout


def licenses(root, env):
    """Full notices for modules used by any payload, Go, and tracked bundled code."""
    result = {}
    inventory = []
    modules = {}
    decoder = json.JSONDecoder()
    for platform in PLATFORMS:
        goos, arch = platform.split("-")
        raw = run(["go", "list", "-mod=readonly", "-deps", "-json", "./cmd/arm"], env={**env, "GOOS": goos, "GOARCH": arch}, timeout=120).decode()
        while raw.strip():
            obj, end = decoder.raw_decode(raw.lstrip())
            raw = raw.lstrip()[end:]
            module = obj.get("Module")
            if module and not module.get("Main"):
                module = module.get("Replace", module)
                modules[module["Path"]] = module
    for name, module in sorted(modules.items()):
        directory = Path(module["Dir"])
        selected = [p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE", "COPYING", "NOTICE"))]
        if not selected:
            raise RuntimeError("missing full license notice for Go module: " + name)
        for path in sorted(selected):
            key = "modules/" + name + "/" + path.name
            result[key] = path.read_bytes()
            inventory.append({"module": name, "version": module.get("Version"), "file": key, "sha256": digest(path)})
    tracked = run(["git", "ls-files", "-z"], timeout=20).split(b"\0")
    for raw in tracked:
        if not raw:
            continue
        relative = Path(os.fsdecode(raw))
        if relative.name.upper().startswith(("LICENSE", "COPYING", "NOTICE")):
            path = root / relative
            if path.is_symlink() or not path.is_file():
                raise RuntimeError("unsafe tracked license entry")
            result["source/" + relative.as_posix()] = path.read_bytes()
    goroot = Path(run(["go", "env", "GOROOT"], env=env, timeout=20).decode().strip())
    result["go/LICENSE"] = (goroot / "LICENSE").read_bytes()
    result["inventory.json"] = (json.dumps(inventory, indent=2, sort_keys=True) + "\n").encode()
    return result


def archive(path, binary, catalog, notices):
    members = {"catalog.json": (json.dumps(catalog, indent=2, sort_keys=True) + "\n").encode(), **{"licenses/" + k: v for k, v in notices.items()}}
    with path.open("xb") as destination, gzip.GzipFile(filename="", mode="wb", fileobj=destination, mtime=0) as compressed, tarfile.open(fileobj=compressed, mode="w|") as tar:
        info = tarfile.TarInfo("arm")
        info.mode = 0o755
        info.size = binary.stat().st_size
        with binary.open("rb") as source:
            tar.addfile(info, source)
        for name, raw in sorted(members.items()):
            info = tarfile.TarInfo(name)
            info.mode = 0o644
            info.size = len(raw)
            tar.addfile(info, io.BytesIO(raw))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tag", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--tagged", action="store_true", help="require exact tag and reviewed-main ancestry")
    args = parser.parse_args()
    release_version = version(args.tag)
    root = Path.cwd()
    commit = run(["git", "rev-parse", "HEAD"], timeout=20).decode().strip()
    if os.environ.get("GITHUB_SHA") and os.environ["GITHUB_SHA"] != commit:
        raise RuntimeError("checkout does not match workflow source")
    run(["git", "diff", "--exit-code"], timeout=20)
    run(["git", "diff", "--cached", "--exit-code"], timeout=20)
    if args.tagged:
        if run(["git", "rev-parse", "refs/tags/" + args.tag + "^{commit}"], timeout=20).decode().strip() != commit:
            raise RuntimeError("tag does not identify this checkout")
        run(["git", "merge-base", "--is-ancestor", commit, "origin/main"], timeout=20)
    if args.output.exists():
        raise RuntimeError("release output already exists; use a fresh directory")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    env = {**os.environ, "GOMAXPROCS": "1", "GOFLAGS": "", "CGO_ENABLED": "0", "GOWORK": "off", "GOTOOLCHAIN": "local"}
    # The source remains unchanged. The builder injects one identity into every binary.
    with tempfile.TemporaryDirectory(prefix="arm-release-", dir=args.output.parent) as temporary:
        stage = Path(temporary)
        carriers = stage / "carriers"
        run(["go", "run", "-mod=readonly", "-p=1", "./scripts/release-build", "--version", release_version, "--output", str(carriers)], env=env, timeout=1800)
        records = json.loads((carriers / "catalogs.json").read_text())
        notices = licenses(root, env)
        output = stage / "assets"
        output.mkdir()
        manifest = {"schema": 1, "tag": args.tag, "version": release_version, "source_commit": commit, "go_version": run(["go", "version"], env=env, timeout=20).decode().strip(), "lifecycle_protocol": records[0]["catalog"]["expected_lifecycle_protocol"], "carriers": []}
        for platform, name, record in zip(PLATFORMS, filenames(args.tag), records):
            if record["os"] + "-" + record["arch"] != platform:
                raise RuntimeError("carrier build output platform mismatch")
            path = output / name
            archive(path, carriers / platform / "arm", record["catalog"], notices)
            manifest["carriers"].append({"platform": platform, "archive": name, "sha256": digest(path), "size_bytes": path.stat().st_size, "embedded_catalog": record["catalog"]})
        (output / "release.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        (output / "SHA256SUMS").write_text("".join(f"{digest(path)}  {path.name}\n" for path in sorted(output.iterdir())))
        verify_directory(output, args.tag, commit)
        output.rename(args.output)
    print("Verified four release carriers, each with four embedded payloads; output ready.")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    main()
