"""Closed release layout and tag validation shared by the build and publisher."""
import hashlib
import json
import re
from pathlib import Path

PLATFORMS = ("darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64")
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?", re.ASCII)

def version(tag):
    match = TAG.fullmatch(tag)
    if not match or len(tag) > 97:
        raise ValueError("release tag must be a full vMAJOR.MINOR.PATCH semantic version")
    for identifier in (match[4] or "").split("."):
        if not identifier or (identifier.isdecimal() and len(identifier) > 1 and identifier[0] == "0"):
            if match[4] is not None:
                raise ValueError("invalid semantic prerelease identifier")
    if match[5] is not None and any(not x for x in match[5].split(".")):
        raise ValueError("invalid semantic build identifier")
    return tag[1:]

def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()

def filenames(tag):
    version(tag)
    return [f"arm-{tag}-{platform}.tar.gz" for platform in PLATFORMS]

def verify_directory(directory, tag, commit):
    """Verify every upload byte and refuse extra files/symlinks or other sources."""
    directory = Path(directory)
    expected = set(filenames(tag) + ["release.json", "SHA256SUMS"])
    if not re.fullmatch("[0-9a-f]{40}", commit):
        raise ValueError("invalid release source commit")
    entries = list(directory.iterdir())
    if {p.name for p in entries} != expected or any(p.is_symlink() or not p.is_file() for p in entries):
        raise ValueError("release directory must contain exactly four carriers, manifest and checksums")
    if (directory / "release.json").stat().st_size > 1024 * 1024 or (directory / "SHA256SUMS").stat().st_size > 4096:
        raise ValueError("release metadata exceeds bounds")
    manifest = json.loads((directory / "release.json").read_text())
    if manifest.get("schema") != 1 or manifest.get("tag") != tag or manifest.get("version") != version(tag) or manifest.get("source_commit") != commit:
        raise ValueError("release source/version mismatch")
    protocol = manifest.get("lifecycle_protocol")
    if type(protocol) is not int or protocol < 1:
        raise ValueError("invalid lifecycle protocol")
    records = manifest.get("carriers", [])
    if [r.get("platform") for r in records] != list(PLATFORMS):
        raise ValueError("release must contain all four delivery platforms")
    for record, name in zip(records, filenames(tag)):
        path = directory / name
        if path.stat().st_size < 1 or path.stat().st_size > 512 * 1024 * 1024:
            raise ValueError("carrier archive exceeds bounds")
        catalog = record.get("embedded_catalog", {})
        payloads = catalog.get("artifacts", [])
        if record.get("archive") != name or record.get("sha256") != digest(path) or record.get("size_bytes") != path.stat().st_size:
            raise ValueError("carrier checksum/size mismatch")
        if catalog.get("schema") != 1 or not catalog.get("integrity_scope"):
            raise ValueError("invalid carrier catalog schema/scope")
        if catalog.get("expected_arm_version") != version(tag) or catalog.get("expected_lifecycle_protocol") != manifest.get("lifecycle_protocol") or len(payloads) != 4:
            raise ValueError("carrier catalog version/protocol mismatch")
        if [p.get("os") + "-" + p.get("arch") for p in payloads] != list(PLATFORMS):
            raise ValueError("each carrier must embed all four payloads")
        for payload, platform in zip(payloads, PLATFORMS):
            if payload.get("name") != "arm-" + platform or any(type(payload.get(key)) is not int or not 0 < payload[key] <= 256 * 1024 * 1024 for key in ("size_bytes", "compressed_size_bytes")):
                raise ValueError("invalid embedded payload identity/size")
            for key in ("sha256", "compressed_sha256"):
                if not re.fullmatch("[0-9a-f]{64}", payload.get(key, "")):
                    raise ValueError("invalid embedded payload digest")
    sums = "".join(f"{digest(directory / name)}  {name}\n" for name in sorted(expected - {"SHA256SUMS"}))
    if (directory / "SHA256SUMS").read_text() != sums:
        raise ValueError("release checksums differ from exact expected files")
    return manifest, [directory / name for name in sorted(expected)]
