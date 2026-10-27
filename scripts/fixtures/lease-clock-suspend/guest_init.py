#!/usr/bin/python3
"""Root bootstrap executed only from a fresh disposable guest's read-only ISO."""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid

MEDIA = Path('/mnt/arm-suspend')
RUNTIME = Path('/run/arm-suspend-guest')
DIAGNOSTIC_FD = os.dup(2)
os.set_inheritable(DIAGNOSTIC_FD, False)
# This best-effort guest journal stream must never delay canonical results.
os.set_blocking(DIAGNOSTIC_FD, False)


def require(value, message):
    if not value:
        raise RuntimeError(message)


def sha(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            digest.update(chunk)
    return digest.hexdigest()


def text(path):
    return Path(path).read_text().strip()


def emit(event, **fields):
    print('ARM_VM_BOOTSTRAP ' + json.dumps(dict(event=event, **fields), sort_keys=True), flush=True)


def diagnostic(event, **fields):
    # Retain cloud-init's independent journal descriptor before redirecting the
    # canonical result stream. This bounded side channel supplies no acceptance.
    data = ('ARM_VM_BOOTSTRAP_DIAGNOSTIC ' + json.dumps(dict(event=event, **fields),
            sort_keys=True) + '\n').encode()
    if len(data) > 2048:
        data = ('ARM_VM_BOOTSTRAP_DIAGNOSTIC ' + json.dumps(dict(event=event[:64],
                diagnostic_truncated=True)) + '\n').encode()
    try:
        os.write(DIAGNOSTIC_FD, data)
    except OSError:
        pass


def main():
    # No power interface is opened before all independent guest bindings pass.
    require(sys.platform == 'linux' and os.uname().machine == 'x86_64' and os.geteuid() == 0,
            'requires the disposable Linux amd64 root guest')
    require(len(sys.argv) == 4, 'requires fresh controller UUID, nonce and mode')
    expected_uuid, nonce, mode = sys.argv[1:]
    require(mode in ('positive', 'monotonic-negative'), 'unknown fixture mode')
    require(str(uuid.UUID(expected_uuid)) == expected_uuid, 'noncanonical VM UUID')
    require(re.fullmatch('[0-9a-f]{64}', nonce), 'invalid fresh nonce')
    require(text('/sys/class/dmi/id/sys_vendor') == 'QEMU' and
            text('/sys/class/dmi/id/product_name') == 'ARM-suspend-fixture-Q35' and
            text('/sys/class/dmi/id/product_uuid').lower() == expected_uuid and
            text('/sys/class/dmi/id/product_serial') == nonce, 'fresh QEMU DMI identity differs')
    require(Path('/proc/1/exe').resolve().name == 'systemd' and text('/proc/1/comm') == 'systemd',
            'requires the fresh image systemd PID 1')
    require(os.stat('/proc/self/ns/pid').st_ino == os.stat('/proc/1/ns/pid').st_ino,
            'guest PID namespace differs from PID 1')
    media_mounts = [line.split() for line in Path('/proc/self/mountinfo').read_text().splitlines()
                    if line.split()[4] == str(MEDIA)]
    require(len(media_mounts) == 1 and 'ro' in media_mounts[0][5].split(',') and
            media_mounts[0][media_mounts[0].index('-') + 1] == 'iso9660',
            'fixture media is not a read-only ISO mount')
    guard_path = MEDIA / 'guard.json'
    guard_sha = sha(guard_path)
    guard = json.loads(guard_path.read_text())
    require(set(guard) == {'uuid', 'nonce', 'disk_serial', 'binary_sha256'} and
            guard['uuid'] == expected_uuid and guard['nonce'] == nonce and
            guard['disk_serial'] == 'arm-' + nonce[:16], 'immutable media guard differs')
    root = os.stat('/')
    device = Path('/sys/dev/block/%d:%d' % (os.major(root.st_dev), os.minor(root.st_dev))).resolve(strict=True)
    if (device / 'partition').exists():
        device = device.parent
    serials = [text(device / name) for name in ('serial', 'device/serial') if (device / name).is_file()]
    require(serials and all(value == guard['disk_serial'] for value in serials),
            'root filesystem is not the fresh dedicated virtual disk')
    binary = MEDIA / 'manager.test'
    before = sha(binary)
    require(before == guard['binary_sha256'], 'read-only fixture binary hash differs')
    require(re.fullmatch('[0-9a-f]{64}', before), 'invalid binary digest')
    with binary.open('rb') as stream:
        header = stream.read(20)
    require(len(header) == 20 and header[:4] == b'\x7fELF' and header[4:7] == b'\x02\x01\x01' and
            int.from_bytes(header[16:18], 'little') in (2, 3) and
            int.from_bytes(header[18:20], 'little') == 62, 'fixture payload is not Linux amd64 ELF')
    boot = text('/proc/sys/kernel/random/boot_id')
    require(str(uuid.UUID(boot)) == boot, 'invalid guest boot identity')
    RUNTIME.mkdir(mode=0o700, exist_ok=False)
    marker = dict(guard, boot_id=boot)
    fd = os.open(RUNTIME / 'marker.json', os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o400)
    with os.fdopen(fd, 'w') as stream:
        json.dump(marker, stream, sort_keys=True)
        stream.write('\n')
    marker_info = (RUNTIME / 'marker.json').lstat()
    require(marker_info.st_uid == 0 and marker_info.st_nlink == 1 and
            stat.S_IMODE(marker_info.st_mode) == 0o400, 'runtime marker ownership differs')
    # A dedicated output-only virtio port avoids login UART hangups. Its host
    # backend is a fresh private file with no input-path; UART is diagnostics.
    endpoint = Path('/dev/virtio-ports/arm.suspend.result').resolve(strict=True)
    require(str(endpoint).startswith('/dev/'), 'result endpoint escaped guest devices')
    serial = os.open(endpoint, os.O_WRONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        info = os.fstat(serial)
        require(stat.S_ISCHR(info.st_mode) and info.st_uid == 0 and
                text('/sys/dev/char/%d:%d/name' % (os.major(info.st_rdev), os.minor(info.st_rdev))) ==
                'arm.suspend.result', 'output endpoint is not the identified guest result port')
        os.dup2(serial, 1)
        os.dup2(serial, 2)
    finally:
        os.close(serial)
    states = text('/sys/power/state').split()
    sleep = text('/sys/power/mem_sleep').split()
    require('mem' in states and any(value.strip('[]') == 'deep' for value in sleep),
            'guest kernel does not offer ACPI deep suspend')
    # This path is inside the independently identified Linux guest, never the controller host.
    Path('/sys/power/mem_sleep').write_text('deep\n')
    require('[deep]' in text('/sys/power/mem_sleep').split(), 'guest did not select deep suspend')
    env = dict(os.environ, ARM_DISPOSABLE_SUSPEND_GUEST='1', ARM_SUSPEND_UUID=expected_uuid,
               ARM_SUSPEND_NONCE=nonce, ARM_SUSPEND_DISK_SERIAL=guard['disk_serial'],
               ARM_SUSPEND_BINARY_SHA256=before, ARM_SUSPEND_MODE=mode, GOMAXPROCS='1',
               HOME=str(RUNTIME), TMPDIR=str(RUNTIME))
    emit('guarded', mode=mode, nonce=nonce, uuid=expected_uuid,
         boot_id=boot, binary_sha256=before, selected_sleep='deep')
    # The outer original QEMU owner bounds the entire guest; no guest PID lookup or signaling.
    pattern = '^TestLeaseClockDisposableGuestS3$' if mode == 'monotonic-negative' else '^TestLeaseClockDisposableGuest(S3|BindingGuards)$'
    process = subprocess.run([str(binary), '-test.v', '-test.run=' + pattern,
                              '-test.timeout=180s'], env=env, cwd=RUNTIME, stdin=subprocess.DEVNULL)
    diagnostic('test_return', mode=mode, exit=process.returncode)
    emit('test_return', mode=mode, nonce=nonce, uuid=expected_uuid, exit=process.returncode)
    after = sha(binary)
    require(before == after and sha(guard_path) == guard_sha, 'guest media changed')
    require(text('/proc/sys/kernel/random/boot_id') == boot, 'guest rebooted during the fixture')
    emit('complete', mode=mode, nonce=nonce, uuid=expected_uuid, exit=process.returncode,
         boot_id=boot, binary_sha256_before=before, binary_sha256_after=after,
         marker_sha256=sha(RUNTIME / 'marker.json'))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        diagnostic('bootstrap_error', error_type=type(error).__name__, error=str(error)[:256],
                   errno=getattr(error, 'errno', None))
        emit('bootstrap_error', error_type=type(error).__name__, error=str(error))
        raise
    finally:
        os.close(DIAGNOSTIC_FD)
