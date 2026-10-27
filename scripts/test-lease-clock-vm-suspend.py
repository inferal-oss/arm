#!/usr/bin/env python3
"""Actual disposable Linux S3 acceptance, controlled from macOS; never sleeps the host.

Run through the project's shared test queue after reviewing the frozen source
manifest. Inputs are a vendor-verified read-only cloud image and installed QEMU.
Two fresh guests run sequentially, with no NIC, host share, credentials or SSH
daemon connection. Raw artifacts belong outside the source checkout.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import resource
import secrets
import shutil
import socket
import stat
import sys
import time
import uuid

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
GUEST_TEST = 'internal/manager/lease_clock_suspend_guest_linux_test.go'
TEST = 'TestLeaseClockDisposableGuestS3'
ORACLE = 'production lease clock excluded real guest suspend'
EXCLUDED = {'.git', '.cache', '.tools', '.tla', '.ci-evidence'}
BAD = re.compile(r'panic:|fatal error:|WARNING: DATA RACE|test timed out|^# ', re.M)
DEADLINE = None


def check_time():
    require(DEADLINE is None or time.monotonic() < DEADLINE, 'gate wall-clock bound exhausted')


def require(value, message):
    if not value:
        raise RuntimeError(message)


def digest(path, kind='sha256'):
    check_time()
    value = hashlib.new(kind)
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b''):
            check_time()
            value.update(chunk)
    return value.hexdigest()


def save(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')
    path.chmod(0o600)


def sources():
    found = {}
    for parent, dirs, files in os.walk(ROOT, followlinks=False):
        check_time()
        parent = Path(parent)
        if parent == ROOT:
            dirs[:] = [name for name in dirs if name not in EXCLUDED]
        for name in list(dirs):
            if (parent / name).is_symlink():
                files.append(name)
                dirs.remove(name)
        for name in files:
            check_time()
            path = parent / name
            relative = path.relative_to(ROOT).as_posix()
            if relative == '.git':
                continue
            info = path.lstat()
            raw = os.readlink(path).encode() if path.is_symlink() else path.read_bytes()
            found[relative] = {'sha256': hashlib.sha256(raw).hexdigest(),
                               'mode': '120000' if path.is_symlink() else
                               ('100755' if info.st_mode & 0o111 else '100644')}
    return found


def file_closure(paths):
    result = {}
    seen = set()
    def add(path):
        check_time()
        path = Path(path)
        name = str(path)
        if name in seen:
            return
        seen.add(name)
        if path.is_symlink():
            result[name + ':link'] = os.readlink(path)
            add(path.resolve(strict=True))
        elif path.is_file():
            result[name] = digest(path)
        elif path.is_dir():
            for child in sorted(path.iterdir()):
                add(child)
        else:
            raise RuntimeError('unreadable selected dependency: ' + name)
    for path in paths:
        add(path)
    return result


class QMP:
    def __init__(self, path, log, owner, timeout):
        self.log, self.owner = log, owner
        self.socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.socket.settimeout(1)
        self.events = []
        self.pending = b''
        self.sequence = 0
        until = time.monotonic() + timeout
        while True:
            owner.check()
            try:
                self.socket.connect(str(path))
                break
            except (FileNotFoundError, ConnectionRefusedError):
                require(time.monotonic() < until, 'owned QMP socket did not become ready')
                time.sleep(.05)
        greeting = self.receive(until)
        require('QMP' in greeting, 'missing QMP greeting')
        self.call('qmp_capabilities')

    def record(self, value):
        with self.log.open('a') as stream:
            stream.write(json.dumps({'host_monotonic': time.monotonic(), 'message': value}) + '\n')

    def receive(self, until):
        while b'\n' not in self.pending:
            check_time()
            require(time.monotonic() < until, 'bounded QMP read expired')
            self.owner.check()
            try:
                data = self.socket.recv(1 << 16)
            except socket.timeout:
                continue
            require(data, 'QMP closed before its expected response')
            self.pending += data
            require(len(self.pending) <= 1 << 20, 'QMP message bound exceeded')
        line, self.pending = self.pending.split(b'\n', 1)
        value = json.loads(line)
        self.record(value)
        if 'event' in value:
            self.events.append(value)
        return value

    def call(self, command):
        require(command in {'qmp_capabilities', 'query-uuid', 'query-name', 'query-current-machine',
                            'query-status', 'system_wakeup', 'quit'}, 'QMP operation is outside fixture scope')
        self.owner.check()
        self.sequence += 1
        number = self.sequence
        request = {'execute': command, 'id': number}
        self.record({'sent': request})
        self.socket.sendall((json.dumps(request) + '\n').encode())
        until = time.monotonic() + 10
        while True:
            value = self.receive(until)
            if value.get('id') == number:
                require('error' not in value and 'return' in value, 'QMP rejected ' + command)
                return value['return']

    def close(self):
        self.socket.close()


def frames(output, prefix):
    result = []
    for line in output.splitlines(keepends=True):
        # The producer may be midway through its final write at this live cut.
        # Completed malformed frames still fail; only the trailing fragment waits.
        if not line.endswith('\n'):
            continue
        line = line.strip()
        if line.startswith(prefix):
            result.append(json.loads(line[len(prefix):]))
    return result


def live_protocol(capture):
    raw = capture.read()
    # Do not decode a character whose final bytes have not been written yet.
    return raw[:raw.rfind(b'\n') + 1].decode('utf-8', errors='strict')


def close_protocol(capture, receipt_path):
    caller_error = sys.exc_info()[1]
    failure = caller_error
    try:
        raw = capture.read()
        save(receipt_path, dict(capture.receipt(), sha256=hashlib.sha256(raw).hexdigest(),
                               scope='sole canonical guest result stream; output-only backend'))
    except BaseException as error:
        if failure is None:
            failure = error
        else:
            failure.protocol_capture_error = str(error)
    finally:
        try:
            capture.close()
        except BaseException as error:
            if failure is None:
                failure = error
            else:
                failure.protocol_close_error = str(error)
    if failure is not None and failure is not caller_error:
        raise failure


def verify_test_roster(output, negative):
    """Count actual verbose test events; no exit-only or partial-suite acceptance."""
    runs = re.findall(r'^=== RUN\s+(\S+)\r?$', output, re.M)
    finishes = re.findall(r'^\s*--- (PASS|FAIL|SKIP): (\S+) \(', output, re.M)
    if negative:
        require(runs == [TEST] and finishes == [('FAIL', TEST)], 'negative exact root failure roster differs')
        return
    guards = 'TestLeaseClockDisposableGuestBindingGuards'
    expected = {TEST, guards}
    expected.update(guards + '/' + name for name in (
        'ARM_DISPOSABLE_SUSPEND_GUEST', 'ARM_SUSPEND_NONCE', 'ARM_SUSPEND_UUID',
        'ARM_SUSPEND_DISK_SERIAL', 'ARM_SUSPEND_BINARY_SHA256'))
    expected.update(guards + '/marker-field-' + str(i) for i in range(5))
    require(len(runs) == len(set(runs)) == 12 and set(runs) == expected and
            len(finishes) == 12 and len(set(finishes)) == 12 and
            set(finishes) == {('PASS', name) for name in expected}, 'positive exact 2-root/12-node roster differs')


def main(opts):
    global DEADLINE
    require(sys.platform == 'darwin' and platform.machine() == 'arm64',
            'controller is restricted to macOS arm64; guest-only Linux power code cannot run here')
    DEADLINE = time.monotonic() + 1650
    os.umask(0o077)
    # Only this gate process and its own future children inherit these controls.
    # Stdio cannot feed the guest serial console; the nonce port is its sole input.
    with open('/dev/null', 'rb') as no_input:
        os.dup2(no_input.fileno(), 0)
    _, hard = resource.getrlimit(resource.RLIMIT_FSIZE)
    file_cap = min(4 << 30, hard) if hard != resource.RLIM_INFINITY else 4 << 30
    resource.setrlimit(resource.RLIMIT_FSIZE, (file_cap, file_cap))
    opts.run_dir = opts.run_dir.resolve()
    for name in ('base_image', 'firmware_code', 'firmware_vars', 'go', 'qemu', 'qemu_img'):
        path = getattr(opts, name).resolve(strict=True)
        require(path.is_file() and not any(c in str(path) for c in (',', '\n', '\0')), 'unsafe asset path')
        setattr(opts, name, path)
    require(not any(c in str(opts.run_dir) for c in (',', '\n', '\0')), 'unsafe artifact path')
    require(digest(opts.manifest) == opts.manifest_sha256, 'reviewed source manifest differs')
    manifest = json.loads(opts.manifest.read_text())
    initial = sources()
    require(initial == manifest['files'], 'source differs from the reviewed manifest')
    require(not opts.run_dir.exists() and not opts.run_dir.is_relative_to(ROOT),
            'requires a new private artifact directory outside the checkout')
    opts.run_dir.mkdir(mode=0o700, parents=True)
    deadline = DEADLINE
    helper_path = ROOT / 'scripts/formal_process.py'
    spec = importlib.util.spec_from_file_location('vm_original_processes', helper_path)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    commands = helper.Command(opts.run_dir, ROOT)
    env = dict(os.environ)
    for key in list(env):
        if key.startswith(('GITHUB_', 'RUNNER_', 'Image', 'GIT_', 'CGO_', 'DYLD_', 'LD_')) or key in {
                'CI', 'GOFLAGS', 'GOWORK', 'GOENV', 'GOTOOLCHAIN', 'GOEXPERIMENT', 'GOOS', 'GOARCH',
                'GOROOT', 'GOTOOLDIR', 'GODEBUG', 'GOCACHE', 'GOPATH', 'GOMODCACHE'}:
            env.pop(key, None)
    go = opts.go.resolve(strict=True)
    env.update(PATH=str(go.parent) + ':/usr/bin:/bin:/usr/sbin:/sbin', GOMAXPROCS='1', GOFLAGS='',
               GOENV='off', GOWORK='off', GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off',
               GOCACHE=str(opts.run_dir / 'go-cache'), CGO_ENABLED='0', GOOS='linux', GOARCH='amd64',
               GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1', GIT_OPTIONAL_LOCKS='0')
    def command(argv, name, timeout=60):
        require(sources() == initial, 'source changed before original command')
        remaining = deadline - time.monotonic() - 30
        require(remaining > 0, 'gate time bound exhausted')
        output = commands.command([str(x) for x in argv], name, env=env, timeout=min(timeout, remaining))
        value = json.loads((opts.run_dir / (name + '-owner.json')).read_text())
        owner = value['owner']
        require(value['actual_exit'] == value['expected_exit'] == 0 and
                value['cleanup_error'] is None and value['cancellation_received'] is None and
                value['cancellation_pending'] is None and owner['native_wait_calls'] == 1 and
                owner['retired'] and owner['reaped'] and not owner['authority'] and
                owner['unknown'] is None and not owner['signals'] and
                owner['post_wait_absence'] == {'pid': 'absent', 'group': 'absent'},
                'original command did not join cleanly: ' + name)
        require(sources() == initial, 'source changed during original command')
        return output
    save(opts.run_dir / 'intent.json', {'base_commit': manifest['base_commit'], 'source_manifest_sha256': opts.manifest_sha256,
         'origin': 'local_macos_controller_linux_qemu_tcg_guest', 'guest_cpus': 1, 'guest_memory_mib': 1024,
         'guest_network': 'none', 'host_shares': False, 'guest_suspend_seconds_minimum': 25,
         'guest_modes': ['monotonic-negative', 'positive'], 'qemu_guests_concurrent_max': 1,
         'queue': 'normal shared FIFO, default limits, no overrides', 'host_power_operations': [],
         'inherited_stdin': '/dev/null', 'per_child_file_size_limit_bytes': file_cap,
         'scope': 'Actual Linux guest ACPI deep S3, not host sleep or QEMU pause.'})
    try:
        base = command(['/usr/bin/git', 'rev-parse', 'HEAD'], 'source-head').strip()
        require(base == manifest['base_commit'], 'source base changed')
        save(opts.run_dir / 'sources-before.json', initial)
        for name in (GUEST_TEST, 'scripts/test-lease-clock-vm-suspend.py',
                     'scripts/fixtures/lease-clock-suspend/guest_init.py',
                     'internal/leaseclock/native_linux.go', 'scripts/formal_process.py'):
            retained = opts.run_dir / 'frozen-source' / name
            retained.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, retained)
            require(digest(retained) == initial[name]['sha256'], 'retained source bytes differ')
        require(digest(opts.base_image, 'sha512') == opts.base_image_sha512, 'cached vendor image checksum differs')
        tools = [go.parents[1], opts.qemu.resolve(strict=True), opts.qemu_img.resolve(strict=True),
                 opts.firmware_code.resolve(strict=True), opts.firmware_vars.resolve(strict=True),
                 Path('/usr/bin/git'), Path('/usr/bin/otool'), Path('/usr/bin/hdiutil'), Path(sys.executable)]
        bootstrap_paths = list(tools)
        bootstrap_tools = file_closure(bootstrap_paths)
        save(opts.run_dir / 'tools-before-bootstrap.json', bootstrap_tools)
        # Record non-system Mach-O dependencies, refusing unresolved loader paths.
        queue = [opts.qemu.resolve(), opts.qemu_img.resolve()]
        seen = set()
        while queue:
            path = queue.pop(0)
            if path in seen:
                continue
            seen.add(path)
            output = command(['/usr/bin/otool', '-L', path], 'macho-deps-' + str(len(seen)))
            for line in output.splitlines()[1:]:
                value = line.strip().split(' (', 1)[0]
                if value.startswith(('/usr/lib/', '/System/')):
                    continue  # Sealed OS libraries are a declared trust boundary.
                require(value.startswith('/'), 'unresolved non-system Mach-O dependency')
                dependency = Path(value).resolve(strict=True)
                if dependency != path:
                    tools.append(dependency)
                    queue.append(dependency)
        resolved = json.loads(command([go, 'env', '-json', 'GOVERSION', 'GOROOT', 'GOMODCACHE', 'GOOS', 'GOARCH',
                                        'GOMOD', 'CGO_ENABLED', 'GOENV', 'GOWORK', 'GOFLAGS'], 'go-environment'))
        require(resolved['GOVERSION'] == 'go1.27.3' and resolved['GOOS'] == 'linux' and
                resolved['GOROOT'] == str(go.parents[1]) and
                resolved['GOARCH'] == 'amd64' and resolved['CGO_ENABLED'] == '0' and
                resolved['GOMOD'] == str(ROOT / 'go.mod') and resolved['GOFLAGS'] == '' and
                resolved['GOENV'] == '' and resolved['GOWORK'] == 'off', 'pinned cross-compilation environment differs')
        tools.append(Path(resolved['GOMODCACHE']))
        require(file_closure(bootstrap_paths) == bootstrap_tools, 'selected tools changed during discovery')
        before_tools = file_closure(tools)
        save(opts.run_dir / 'tools-before.json', before_tools)
        command([opts.qemu, '--version'], 'qemu-version')
        command([opts.qemu_img, '--version'], 'qemu-img-version')
        positive = opts.run_dir / 'positive.test'
        negative = opts.run_dir / 'monotonic-negative.test'
        command([go, 'test', '-c', '-p=1', '-o', positive, './internal/manager'], 'positive-build', timeout=420)
        original = ROOT / 'internal/leaseclock/native_linux.go'
        raw = original.read_bytes()
        old = b'unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts)'
        require(raw.count(old) == 1, 'native clock mutation anchor changed')
        mutated = raw.replace(old, b'unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)', 1)
        mutation = opts.run_dir / 'negative-native-linux.go'
        mutation.write_bytes(mutated)
        overlay = opts.run_dir / 'overlay.json'
        save(overlay, {'Replace': {str(original): str(mutation)}})
        command([go, 'test', '-c', '-p=1', '-overlay=' + str(overlay), '-o', negative,
                 './internal/manager'], 'negative-build', timeout=240)
        save(opts.run_dir / 'negative-source.json', {'original_sha256': hashlib.sha256(raw).hexdigest(),
             'generated_sha256': digest(mutation), 'overlay_sha256': digest(overlay),
             'only_change': 'Production native Read CLOCK_BOOTTIME replaced with CLOCK_MONOTONIC; independent guest raw-clock oracle unchanged.'})
        results = []
        for mode, binary, expected_exit in [('monotonic-negative', negative, 1), ('positive', positive, 0)]:
            require(time.monotonic() < deadline - 180, 'insufficient bound for another fresh VM')
            work = opts.run_dir / mode
            work.mkdir(mode=0o700)
            nonce, guest_uuid = secrets.token_hex(32), str(uuid.uuid4())
            vm_name = 'arm-s3-' + nonce[:16]
            serial = 'arm-' + nonce[:16]
            seed = work / 'seed'
            seed.mkdir(mode=0o700)
            shutil.copyfile(binary, seed / 'manager.test')
            (seed / 'manager.test').chmod(0o755)
            shutil.copyfile(ROOT / 'scripts/fixtures/lease-clock-suspend/guest_init.py', seed / 'guest_init.py')
            save(seed / 'guard.json', {'uuid': guest_uuid, 'nonce': nonce, 'disk_serial': serial,
                                       'binary_sha256': digest(binary)})
            # Cloud-init executes this only in the fresh guest; no host shell executes it.
            guest_shell = ('set -eu; test "$(cat /sys/class/dmi/id/product_serial)" = ' + nonce +
                           '; mkdir -p /mnt/arm-suspend; mount -t iso9660 -o ro,uid=0,gid=0,norock /dev/disk/by-label/CIDATA /mnt/arm-suspend; '
                           'exec /usr/bin/python3 /mnt/arm-suspend/guest_init.py ' + guest_uuid + ' ' + nonce + ' ' + mode)
            (seed / 'meta-data').write_text('instance-id: ' + vm_name + '\nlocal-hostname: arm-suspend-fixture\n')
            (seed / 'user-data').write_text('#cloud-config\nruncmd:\n  - ' + json.dumps(['/bin/sh', '-c', guest_shell]) + '\n')
            iso, disk, variables = work / 'seed.iso', work / 'disk.qcow2', work / 'vars.fd'
            command(['/usr/bin/hdiutil', 'makehybrid', '-iso', '-joliet', '-default-volume-name', 'CIDATA',
                     '-o', iso, seed], mode + '-seed', timeout=60)
            command([opts.qemu_img, 'create', '-f', 'qcow2', '-F', 'qcow2', '-b', opts.base_image,
                     disk], mode + '-overlay')
            shutil.copyfile(opts.firmware_vars, variables)
            qmp_path, control_path = work / 'qmp.sock', work / 'control.sock'
            require(len(str(qmp_path).encode()) < 100 and len(str(control_path).encode()) < 100,
                    'private Unix socket path is too long')
            argv = [str(opts.qemu), '-no-user-config', '-nodefaults', '-machine', 'q35,accel=tcg', '-cpu', 'max',
                    '-smp', '1', '-m', '1024', '-name', vm_name, '-uuid', guest_uuid,
                    '-smbios', 'type=1,manufacturer=QEMU,product=ARM-suspend-fixture-Q35,serial=' + nonce,
                    '-display', 'none', '-serial', 'stdio', '-monitor', 'none', '-nic', 'none',
                    '-rtc', 'base=utc,clock=rt,driftfix=none', '-no-reboot',
                    '-drive', 'if=pflash,format=raw,readonly=on,file=' + str(opts.firmware_code),
                    '-drive', 'if=pflash,format=raw,file=' + str(variables),
                    '-drive', 'if=none,id=armdisk,format=qcow2,file=' + str(disk),
                    '-device', 'virtio-blk-pci,drive=armdisk,serial=' + serial,
                    '-drive', 'if=none,id=seed,format=raw,media=cdrom,readonly=on,file=' + str(iso),
                    '-device', 'ide-cd,drive=seed,bus=ide.0',
                    '-qmp', 'unix:' + str(qmp_path) + ',server=on,wait=off',
                    '-chardev', 'socket,id=armcontrol,path=' + str(control_path) + ',server=on,wait=off',
                    '-chardev', 'file,id=armresult,path=' + str(work / 'protocol.log'),
                    '-device', 'virtio-serial-pci', '-device', 'virtserialport,chardev=armcontrol,name=arm.suspend.control',
                    '-device', 'virtserialport,chardev=armresult,name=arm.suspend.result']
            save(work / 'launch.json', {'argv': argv, 'uuid': guest_uuid, 'nonce': nonce, 'binary_sha256': digest(binary)})
            protocol = helper.Capture(work / 'protocol.log', exclusive=True)
            try:
                fixture = helper.Fixture(argv, work / 'qemu.log', ROOT, env=env)
            except BaseException:
                close_protocol(protocol, work / 'protocol-capture.json')
                raise
            qmp = control = None
            qmp_verified = False
            try:
                qmp = QMP(qmp_path, work / 'qmp.jsonl', fixture.owner, 30)
                require(qmp.call('query-uuid')['UUID'].lower() == guest_uuid and
                        qmp.call('query-name')['name'] == vm_name and
                        qmp.call('query-current-machine')['wakeup-suspend-support'] is True,
                        'owned QEMU identity or S3 wake support differs')
                qmp_verified = True
                control = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                control.settimeout(5)
                control.connect(str(control_path))
                until = min(deadline - 60, time.monotonic() + 240)
                while True:
                    output = live_protocol(protocol)
                    bootstrap = frames(output, 'ARM_VM_BOOTSTRAP ')
                    require(not any(e['event'] == 'bootstrap_error' for e in bootstrap), 'guest bootstrap refused its guard')
                    guest = frames(output, 'ARM_VM_SUSPEND_EVENT ')
                    before = [e for e in guest if e.get('event') == 'before_sleep']
                    if before:
                        require(len(before) == 1 and before[0]['nonce'] == nonce, 'foreign/duplicate guest arm event')
                        break
                    require(fixture.terminal() is None and time.monotonic() < until,
                            'guest did not reach its bounded S3 arm point')
                    time.sleep(.1)
                fixture.owner.check()
                cycle_start = len(qmp.events)
                control.sendall(('ARM_S3_ARM ' + nonce + '\n').encode())
                until = time.monotonic() + 30
                while not any(e.get('event') == 'SUSPEND' for e in qmp.events):
                    qmp.receive(until)
                require(qmp.call('query-status')['status'] == 'suspended', 'QEMU did not enter suspended state')
                suspended = time.monotonic()
                while time.monotonic() - suspended < 25:
                    require(qmp.call('query-status')['status'] == 'suspended', 'guest woke before the external hold completed')
                    time.sleep(.25)
                held = time.monotonic() - suspended
                qmp.call('system_wakeup')
                until = time.monotonic() + 30
                while not any(e.get('event') == 'WAKEUP' for e in qmp.events):
                    qmp.receive(until)
                require(qmp.call('query-status')['status'] == 'running', 'QEMU did not return to running state')
                until = min(deadline - 30, time.monotonic() + 180)
                while True:
                    output = live_protocol(protocol)
                    bootstrap = frames(output, 'ARM_VM_BOOTSTRAP ')
                    require(not any(e['event'] == 'bootstrap_error' for e in bootstrap), 'guest bootstrap failed')
                    completed = [e for e in bootstrap if e.get('event') == 'complete']
                    if completed:
                        require(len(completed) == 1, 'duplicate guest completion')
                        report = completed[0]
                        break
                    require(fixture.terminal() is None and time.monotonic() < until, 'guest completion did not arrive')
                    time.sleep(.1)
                require(report['nonce'] == nonce and report['uuid'] == guest_uuid and report['exit'] == expected_exit and
                        report['binary_sha256_before'] == report['binary_sha256_after'] == digest(binary),
                        'actual guest exit or executable provenance differs')
                returned = [e for e in bootstrap if e.get('event') == 'test_return']
                require(len(returned) == 1 and returned[0] == dict(event='test_return', mode=mode,
                        nonce=nonce, uuid=guest_uuid, exit=expected_exit), 'original guest test return differs')
                require(not BAD.search(output), 'panic/build/timeout cannot satisfy guest acceptance')
                verify_test_roster(output, bool(expected_exit))
                diagnostics = re.findall(r'^\s*([\w.-]+_test\.go):\d+: (.+)$', output, re.M)
                if expected_exit:
                    require(diagnostics == [('lease_clock_suspend_guest_linux_test.go', ORACLE)] and
                            '--- FAIL: ' + TEST in output and '\nFAIL' in output,
                            'negative guest lacks its sole causal failure')
                else:
                    require('--- PASS: ' + TEST in output and '\nPASS' in output and
                            '--- FAIL:' not in output and '--- SKIP:' not in output,
                            'positive guest did not pass its exact root')
                names = [e['event'] for e in qmp.events[cycle_start:]]
                require(names.count('SUSPEND') == names.count('WAKEUP') == 1 and
                        not set(names).intersection({'STOP', 'RESET', 'SUSPEND_DISK'}), 'guest lifecycle differs from one S3 cycle')
                save(work / 'guest-result.json', {'mode': mode, 'expected_exit': expected_exit,
                     'external_suspended_seconds': held, 'qmp_events': qmp.events, 'guest_report': report,
                     'guest_events': frames(output, 'ARM_VM_SUSPEND_EVENT '),
                     'guest_stages': frames(output, 'ARM_VM_SUSPEND_STAGE '), 'binary_sha256': digest(binary)})
                qmp.call('quit')
                actual_exit, _ = fixture.wait(15)
                require(actual_exit == 0, 'original QEMU did not exit normally')
                final_raw = protocol.read()
                require(final_raw.endswith(b'\n') and final_raw.decode('utf-8', errors='strict') == output,
                        'canonical guest output changed after completion or ended incompletely')
                shutil.copyfile(work / 'qemu.owner.json', work / 'qemu-wait-owner.json')
                results.append({'mode': mode, 'guest_exit': expected_exit, 'qemu_exit': actual_exit,
                                'external_suspended_seconds': held, 'binary_sha256': digest(binary)})
            finally:
                # Use the already verified original QMP connection before
                # retiring it. An error must not turn a graceful VM shutdown
                # into an unnecessary TERM/KILL race with its original waiter.
                caller_error = sys.exc_info()[1]
                try:
                    if qmp_verified and fixture.owner.authority:
                        try:
                            qmp.call('quit')
                            stopped, _ = fixture.wait(15)
                            shutil.copyfile(work / 'qemu.owner.json', work / 'qemu-wait-owner.json')
                            save(work / 'failure-qmp-shutdown.json', {'status': 'original_qemu_joined', 'exit': stopped})
                        except Exception as cleanup_error:
                            try:
                                save(work / 'failure-qmp-shutdown.json', {'status': 'unconfirmed',
                                     'error_type': type(cleanup_error).__name__, 'error': str(cleanup_error)})
                            except Exception as report_error:
                                if caller_error is None:
                                    raise
                                caller_error.qmp_shutdown_report_error = str(report_error)
                finally:
                    try:
                        if control is not None:
                            control.close()
                    finally:
                        try:
                            if qmp is not None:
                                qmp.close()
                        finally:
                            try:
                                fixture.close()  # Retained fresh authority only, even if reporting failed.
                            finally:
                                close_protocol(protocol, work / 'protocol-capture.json')
            close_receipt = json.loads((work / 'qemu.owner.json').read_text())
            owner = close_receipt['owner']
            require(close_receipt['native_join_confirmed'] and close_receipt['cleanup_error'] is None and
                    owner['native_wait_calls'] == 1 and owner['retired'] and owner['reaped'] and
                    not owner['authority'] and owner['unknown'] is None and not owner['signals'] and
                    owner['post_wait_absence'] == {'pid': 'absent', 'group': 'absent'},
                    'original QEMU owner did not retire and join cleanly')
            require(disk.stat().st_blocks * 512 <= 4 << 30, 'fresh overlay exceeded allocated disk budget')
            require(sources() == initial, 'source changed during VM execution')
        after_tools = file_closure(tools)
        save(opts.run_dir / 'tools-after.json', after_tools)
        save(opts.run_dir / 'sources-after.json', sources())
        require(after_tools == before_tools and sources() == initial and
                digest(opts.base_image, 'sha512') == opts.base_image_sha512, 'read-only inputs changed')
        require(time.monotonic() < deadline, 'gate finalization exceeded its declared bound')
        save(opts.run_dir / 'result.json', {'status': 'passed_actual_guest_s3', 'base_commit': base,
             'source_manifest_sha256': opts.manifest_sha256, 'source_files': len(initial),
             'selected_tool_entries': len(before_tools), 'source_before_after_equal': True,
             'tools_before_after_equal': True, 'read_only_base_sha512': opts.base_image_sha512,
             'guest_results': results, 'host_power_operations': [], 'all_guests_positively_joined': True,
             'scope': 'Two actual Linux amd64 Q35/TCG S3 cycles; no physical host or Darwin sleep claim.'})
        check_time()
        print('PASS two actual disposable Linux S3 cycles, exact causal negative and positive Manager acceptance', flush=True)
    except BaseException as error:
        snapshots = {}
        if time.monotonic() < deadline:
            try:
                save(opts.run_dir / 'sources-on-failure.json', sources())
                snapshots['sources'] = 'retained'
            except Exception as capture_error:
                snapshots['sources'] = 'unavailable: ' + str(capture_error)
        save(opts.run_dir / 'result.json', {'status': 'failed', 'error_type': type(error).__name__,
             'error': str(error), 'failure_snapshots': snapshots})
        raise


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('base-image', 'firmware-code', 'firmware-vars', 'go', 'qemu', 'qemu-img', 'manifest', 'run-dir'):
        parser.add_argument('--' + name, type=Path, required=True)
    parser.add_argument('--base-image-sha512', required=True)
    parser.add_argument('--manifest-sha256', required=True)
    main(parser.parse_args())
