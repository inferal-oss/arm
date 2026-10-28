#!/usr/bin/env python3
"""Run the XFS quota fixture in one fresh, offline QEMU guest.

This runner creates a qcow2 overlay and cloud-init seed under an explicit new
private directory. It never runs the guest's mkfs/mount commands on the host.
The original QEMU Popen object is waited and reaped; no saved PID is adopted.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tarfile
import time

VENDOR_SHA512 = "f46f0671a6e5bdec5291ab8972bae2f10e5408c2f64a74078f11efc2f06a436a9d0313ed50e0472542eeabf780e9f7c792ac0a314c6c20507fcd9fd81b468c3d"
XFSPROGS_SHA256 = "79e00160bfc45d25a46d2709edd364afb41723d9b96713de8f6c80b6a17f2d74"
LIBINIH_SHA256 = "22e51939acb45906a57851508290782a2a704fce5208e213e951268b3c799c4a"


def digest(path, algorithm="sha256"):
    h = hashlib.new(algorithm)
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def command(argv, timeout=90):
    return subprocess.run(argv, check=True, timeout=timeout, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True).stdout


def package_file(package, member):
    data = subprocess.run(["/usr/bin/ar", "-p", str(package), "data.tar.xz"],
                          check=True, stdout=subprocess.PIPE, timeout=90).stdout
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:xz") as archive:
        entry = archive.getmember(member)
        if not entry.isfile():
            raise RuntimeError("pinned package entry is not a regular file: " + member)
        stream = archive.extractfile(entry)
        if stream is None:
            raise RuntimeError("pinned package entry is unreadable: " + member)
        return stream.read()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-image", type=Path, required=True)
    parser.add_argument("--test-binary", type=Path, required=True)
    parser.add_argument("--xfsprogs-deb", type=Path, required=True)
    parser.add_argument("--libinih-deb", type=Path, required=True)
    parser.add_argument("--run-dir", type=Path, required=True)
    parser.add_argument("--timeout", type=int, default=600)
    args = parser.parse_args()
    if args.timeout < 60 or args.timeout > 900:
        raise RuntimeError("bounded guest timeout must be 60..900 seconds")
    base = args.base_image.resolve(strict=True)
    binary = args.test_binary.resolve(strict=True)
    xfsprogs = args.xfsprogs_deb.resolve(strict=True)
    libinih = args.libinih_deb.resolve(strict=True)
    if digest(base, "sha512") != VENDOR_SHA512:
        raise RuntimeError("vendor Debian image hash differs")
    if not os.access(binary, os.X_OK):
        raise RuntimeError("compiled guest test is not executable")
    if digest(xfsprogs) != XFSPROGS_SHA256:
        raise RuntimeError("Debian xfsprogs package hash differs")
    if digest(libinih) != LIBINIH_SHA256:
        raise RuntimeError("Debian XFS dependency hash differs")
    run = args.run_dir.resolve()
    run.mkdir(mode=0o700)
    if any(run.iterdir()):
        raise RuntimeError("run directory must be new and empty")
    run.chmod(0o700)
    nonce = secrets.token_hex(24)
    seed = run / "seed"
    seed.mkdir(mode=0o700)
    shutil.copy2(binary, seed / "manager.test")
    guest_script = Path(__file__).with_name("guest-run.sh")
    shutil.copy2(guest_script, seed / "guest-run.sh")
    native_tools = seed / "native-tools"
    native_tools.mkdir(mode=0o700)
    for name, package, member in (
        ("mkfs.xfs", xfsprogs, "./usr/sbin/mkfs.xfs"),
        ("xfs_quota", xfsprogs, "./usr/sbin/xfs_quota"),
        ("libinih.so.1", libinih, "./usr/lib/x86_64-linux-gnu/libinih.so.58"),
    ):
        target = native_tools / name
        target.write_bytes(package_file(package, member))
        target.chmod(0o700 if name != "libinih.so.1" else 0o600)
    shell = (
        "set -u; test \"$(cat /sys/class/dmi/id/product_serial)\" = " + nonce +
        "; mkdir -p /mnt/arm-sync-seed; mount -t iso9660 -o ro,uid=0,gid=0,norock "
        "/dev/disk/by-label/CIDATA /mnt/arm-sync-seed; "
        "cp /mnt/arm-sync-seed/manager.test /root/arm-sync-manager.test; "
        "cp /mnt/arm-sync-seed/guest-run.sh /root/arm-sync-guest-run.sh; "
        "mkdir -m 0700 /root/arm-sync-native-tools; "
        "cp /mnt/arm-sync-seed/native-tools/* /root/arm-sync-native-tools/; "
        "chmod 700 /root/arm-sync-manager.test /root/arm-sync-guest-run.sh; "
        "chmod 700 /root/arm-sync-native-tools/mkfs.xfs /root/arm-sync-native-tools/xfs_quota; "
        "systemctl stop serial-getty@ttyS0.service; "
        "systemctl mask --runtime serial-getty@ttyS0.service; "
        "ARM_SYNC_XFS_GUEST_NONCE=" + nonce +
        " /bin/sh /root/arm-sync-guest-run.sh /root/arm-sync-manager.test /root/arm-sync-native-tools > /dev/ttyS0 2>&1; "
        "result=$?; echo ARM_SYNC_XFS_RESULT nonce=" + nonce +
        " exit=$result > /dev/ttyS0; /sbin/poweroff -f; exit $result"
    )
    (seed / "meta-data").write_text("instance-id: arm-sync-xfs-" + nonce + "\nlocal-hostname: arm-sync-xfs\n")
    (seed / "user-data").write_text("#cloud-config\nruncmd:\n  - " + json.dumps(["/bin/sh", "-c", shell]) + "\n")
    iso = run / "seed.iso"
    disk = run / "disk.qcow2"
    variables = run / "vars.fd"
    code = Path("/opt/homebrew/share/qemu/edk2-x86_64-code.fd")
    var_template = Path("/opt/homebrew/share/qemu/edk2-i386-vars.fd")
    if not code.is_file() or not var_template.is_file():
        raise RuntimeError("QEMU firmware files are unavailable")
    command(["/usr/bin/hdiutil", "makehybrid", "-iso", "-joliet", "-default-volume-name",
             "CIDATA", "-o", str(iso), str(seed)])
    command(["/opt/homebrew/bin/qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b",
             str(base), str(disk)])
    shutil.copyfile(var_template, variables)
    argv = ["/opt/homebrew/bin/qemu-system-x86_64", "-no-user-config", "-nodefaults",
            "-machine", "q35,accel=tcg", "-cpu", "max", "-smp", "4", "-m", "3072",
            "-name", "arm-sync-xfs-" + nonce[:12], "-smbios",
            "type=1,manufacturer=QEMU,product=ARM-sync-XFS,serial=" + nonce,
            "-display", "none", "-serial", "file:" + str(run / "serial.log"),
            "-monitor", "none", "-nic", "none", "-no-reboot",
            "-drive", "if=pflash,format=raw,readonly=on,file=" + str(code),
            "-drive", "if=pflash,format=raw,file=" + str(variables),
            "-drive", "if=none,id=armdisk,format=qcow2,file=" + str(disk),
            "-device", "virtio-blk-pci,drive=armdisk",
            "-drive", "if=none,id=seed,format=raw,media=cdrom,readonly=on,file=" + str(iso),
            "-device", "ide-cd,drive=seed,bus=ide.0"]
    manifest = {"base_sha512": VENDOR_SHA512, "test_sha256": digest(binary),
                "xfsprogs_sha256": XFSPROGS_SHA256,
                "libinih_sha256": LIBINIH_SHA256,
                "guest_script_sha256": digest(guest_script), "nonce": nonce,
                "offline": True, "fresh_overlay": str(disk), "argv": argv}
    (run / "launch.json").write_text(json.dumps(manifest, indent=2) + "\n")
    started = time.monotonic()
    with (run / "qemu.log").open("wb") as output:
        child = subprocess.Popen(argv, stdout=output, stderr=subprocess.STDOUT,
                                 stdin=subprocess.DEVNULL, start_new_session=True)
        try:
            exit_code = child.wait(timeout=args.timeout)
        except subprocess.TimeoutExpired:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=10)
            raise RuntimeError("original QEMU process exceeded guest timeout")
    serial = (run / "serial.log").read_text(errors="replace") if (run / "serial.log").exists() else ""
    marker = "ARM_SYNC_XFS_RESULT nonce=" + nonce + " exit=0"
    result = {"qemu_original_exit": exit_code, "qemu_reaped": child.poll() is not None,
              "elapsed_seconds": round(time.monotonic() - started, 3),
              "serial_sha256": digest(run / "serial.log") if (run / "serial.log").exists() else None,
              "guest_success_marker": marker in serial,
              "guest_test_pass": "--- PASS: TestNativeChargedScopeGuest" in serial,
              "guest_quota_refusal": "ARM_SYNC_NATIVE_QUOTA_REFUSAL" in serial}
    (run / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    if exit_code != 0 or not result["guest_success_marker"] or not result["guest_test_pass"] or not result["guest_quota_refusal"]:
        raise RuntimeError("guest did not complete all native quota/refusal controls; preserve run directory")
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print("XFS guest fixture failed: " + str(exc), file=sys.stderr)
        sys.exit(1)
