#!/usr/bin/env python3
"""Provision checksum-pinned local TLA+ tools, never global packages or PATH edits."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import stat
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
RECEIPT = ".arm-tool.json"
MAX_EXPANDED = 256 << 20
MAX_MEMBERS = 512


def sha(data):
    return hashlib.sha256(data).hexdigest()


def directory(path):
    try:
        path.mkdir(mode=0o700)
    except FileExistsError:
        pass
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
        raise ValueError(f"refusing non-directory or foreign owner: {path}")


def download(artifact, target):
    """An existing mismatch is evidence to inspect, not something to overwrite."""
    if target.exists() or target.is_symlink():
        info = target.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size != artifact["bytes"] or sha(target.read_bytes()) != artifact["sha256"]:
            raise ValueError(f"cached artifact differs from lock; inspect {target}")
        return target
    request = urllib.request.Request(artifact["url"], headers={"User-Agent": "arm-tla-development-tools"})
    with tempfile.NamedTemporaryFile(dir=target.parent, prefix=".download-", delete=False) as output:
        temporary = Path(output.name)
        digest = hashlib.sha256()
        count = 0
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                while chunk := response.read(1 << 20):
                    count += len(chunk)
                    if count > artifact["bytes"]:
                        raise ValueError("download exceeds pinned artifact size")
                    digest.update(chunk)
                    output.write(chunk)
            output.flush()
            os.fsync(output.fileno())
            if count != artifact["bytes"] or digest.hexdigest() != artifact["sha256"]:
                raise ValueError("download size or SHA-256 differs from tools.lock.json")
            # link is no-replace; another installer cannot be silently overwritten.
            os.link(temporary, target)
        finally:
            temporary.unlink()
    return target


def extract(archive, destination):
    """Only bounded, unique regular files/directories within the official root."""
    with tarfile.open(archive, "r:gz") as bundle:
        members = []
        names = set()
        total = 0
        for item in bundle:
            path = PurePosixPath(item.name)
            if item.name != str(path) or path.is_absolute() or not path.parts or path.parts[0] != "tlapm" or any(part in (".", "..") for part in path.parts) or item.name in names:
                raise ValueError("unsafe or duplicate archive path")
            if not (item.isdir() or item.isreg()) or item.mode & 0o7000:
                raise ValueError("archive links, special files or special permissions refused")
            names.add(item.name)
            total += item.size
            if len(names) > MAX_MEMBERS or item.size < 0 or item.size > 64 << 20 or total > MAX_EXPANDED:
                raise ValueError("archive member/expanded size limit exceeded")
            members.append(item)
        if not members:
            raise ValueError("empty tool archive")
        for item in members:
            path = destination.joinpath(*PurePosixPath(item.name).parts)
            if item.isdir():
                path.mkdir(parents=True, exist_ok=True, mode=0o755)
            else:
                path.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
                source = bundle.extractfile(item)
                if source is None:
                    raise ValueError("missing archive payload")
                with source, path.open("xb") as output:
                    shutil.copyfileobj(source, output, length=1 << 20)
                path.chmod(0o755 if item.mode & 0o111 else 0o644)


def inventory(root):
    found = {}
    for path in root.rglob("*"):
        info = path.lstat()
        name = path.relative_to(root).as_posix()
        if stat.S_ISDIR(info.st_mode):
            found[name] = {"directory": True}
        elif stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
            if name != RECEIPT:
                found[name] = {"sha256": sha(path.read_bytes()), "mode": stat.S_IMODE(info.st_mode)}
        else:
            raise ValueError("installed tool contains a link or special file")
    return found


def install(artifact, cached, destination, version, archive):
    expected = {"schema": 1, "version": version, "archive_sha256": artifact["sha256"]}
    if destination.exists() or destination.is_symlink():
        directory(destination)
        receipt = destination / RECEIPT
        if receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_size > 128 << 10:
            raise ValueError(f"unrecognized tool destination; inspect {destination}")
        saved = json.loads(receipt.read_text())
        if {k: saved.get(k) for k in expected} != expected or saved.get("files") != inventory(destination):
            raise ValueError(f"installed tool differs; inspect {destination}")
        return
    temporary = Path(tempfile.mkdtemp(prefix=".install-", dir=destination.parent))
    try:
        if archive:
            extract(cached, temporary)
        else:
            shutil.copyfile(cached, temporary / "tla2tools.jar")
        expected["files"] = inventory(temporary)
        (temporary / RECEIPT).write_text(json.dumps(expected, indent=2) + "\n")
        temporary.rename(destination)
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--component", choices=("all", "tlc", "tlapm"), default="all")
    opts = parser.parse_args()
    lock = json.loads((ROOT / "formal/tools.lock.json").read_text())
    if lock["schema"] != 1:
        parser.error("unknown tool lock schema")
    base = ROOT / ".tla"
    directory(base)
    for name in ("downloads", "tools", "states", "traces", "proofs"):
        directory(base / name)
    key = platform.system().lower() + "-" + {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "AMD64": "amd64"}.get(platform.machine(), platform.machine())
    if opts.component in ("all", "tlapm") and key not in lock["tlapm"]["artifacts"]:
        parser.error(f"no pinned upstream TLAPM binary for {key}; use make tla-tlc for TLC, and supply an independently installed TLAPM to proof checks (see formal/README.md)")
    for kind in ("tlc", "tlapm"):
        if opts.component not in ("all", kind):
            continue
        entry = lock[kind]
        artifact = entry["artifact"] if kind == "tlc" else entry["artifacts"][key]
        name = artifact["url"].rsplit("/", 1)[-1]
        cached = download(artifact, base / "downloads" / name)
        destination = base / "tools" / kind
        install(artifact, cached, destination, entry["version"], archive=kind == "tlapm")
        print(f"{kind} {entry['version']}: verified {destination}")
    if opts.component in ("all", "tlc") and not shutil.which("java"):
        parser.error("TLC is provisioned; install/provide a Java runtime on PATH before running it")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, tarfile.TarError) as error:
        raise SystemExit(str(error))
