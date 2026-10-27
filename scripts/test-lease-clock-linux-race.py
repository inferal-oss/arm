#!/usr/bin/env python3
"""Bounded native Linux race/recovery evidence; no VM or power operations.

The tested source is immutable checkout A. This runner is invoked from its
workflow-only direct child B, and its runner/helper/roster bytes must equal A.
All native commands retain scripts/formal_process.py's original owner receipts.
"""
import argparse
import collections
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import stat
import sys
import time

sys.dont_write_bytecode = True
SELF = Path(__file__).resolve()
HARNESS = SELF.parents[1]
RUNNER_REL = 'scripts/test-lease-clock-linux-race.py'
HELPER_REL = 'scripts/formal_process.py'
HELPER_SHA = '221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76'
WORKFLOW_REL = '.github/workflows/lease-clock-linux-race.yml'
MANAGER = 'getarm.org/internal/manager'
CLOCK = 'getarm.org/internal/leaseclock'
S3_ROOT = 'TestLeaseClockDisposableGuestS3'
HEADER_NAMES = ('stdlib.h', 'stdint.h', 'stddef.h', 'stdio.h', 'string.h', 'errno.h',
                'pthread.h', 'signal.h', 'unistd.h', 'time.h', 'dlfcn.h', 'sys/types.h',
                'sys/mman.h', 'sys/resource.h', 'sys/socket.h', 'netdb.h',
                'netinet/in.h', 'resolv.h')
COMPILER_PROGS = ('cc1', 'collect2', 'as', 'ld')
COMPILER_FILES = ('libgcc.a', 'libgcc_s.so.1', 'libc.so.6', 'libm.so.6',
                  'libpthread.so.0', 'libdl.so.2', 'libresolv.so.2', 'ld-linux-x86-64.so.2')
BAD_OUTPUT = re.compile(r'panic:|fatal error:|WARNING: DATA RACE|test timed out|^# ', re.M)
TRUST = ('The selected GCC driver, resolved compiler/assembler/linker programs, '
         'queried runtime files, intrinsic include directory and the fixed native '
         'header probe closure are content-bound. This is not a universal compiler '
         'closure: the hosted Ubuntu image, kernel, sealed/system services, dynamic '
         'system-library loading and unlisted native headers/libraries remain trusted.')


def require(value, reason):
    if not value:
        raise ValueError(reason)


def check_time():
    if time.monotonic() >= STATE['deadline']:
        raise TimeoutError('native Linux gate exceeded its 1200-second bound')


def digest(path):
    check_time()
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            check_time()
            h.update(chunk)
    return h.hexdigest()


def save(name, value):
    p = STATE['output'] / name
    p.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    p.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')
    p.chmod(0o600)


def file_record(path):
    check_time()
    before = path.lstat()
    if stat.S_ISLNK(before.st_mode):
        raw = os.readlink(path).encode()
        mode = '120000'
        size = len(raw)
        content = hashlib.sha256(raw).hexdigest()
        blob = hashlib.sha1(b'blob ' + str(size).encode() + b'\0' + raw).hexdigest()
    else:
        require(stat.S_ISREG(before.st_mode), 'source is not a regular file/symlink')
        mode = '100755' if before.st_mode & 0o111 else '100644'
        content_hash = hashlib.sha256()
        git_hash = hashlib.sha1(b'blob ' + str(before.st_size).encode() + b'\0')
        size = 0
        with path.open('rb') as f:
            for chunk in iter(lambda: f.read(1 << 20), b''):
                check_time()
                content_hash.update(chunk)
                git_hash.update(chunk)
                size += len(chunk)
        require(size == before.st_size, 'source size changed during read')
        content, blob = content_hash.hexdigest(), git_hash.hexdigest()
    after = path.lstat()
    require((before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_mode) ==
            (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_mode),
            'source identity changed during read')
    return dict(mode=mode, bytes=size, sha256=content, git_blob_sha1=blob)


def snapshot(root):
    found = {}
    for directory, dirs, files in os.walk(root, followlinks=False):
        check_time()
        parent = Path(directory)
        if parent == root:
            dirs[:] = [n for n in dirs if n != '.git']
        for n in list(dirs):
            if (parent / n).is_symlink():
                files.append(n)
                dirs.remove(n)
        for n in files:
            rel = (parent / n).relative_to(root).as_posix()
            if rel != '.git':
                found[rel] = file_record(parent / n)
    return found


def closure(paths):
    found, seen = {}, set()
    def add(p):
        check_time()
        p = Path(p)
        key = str(p)
        if key in seen:
            return
        seen.add(key)
        s = p.lstat()
        if stat.S_ISLNK(s.st_mode):
            found[key + ':link'] = os.readlink(p)
            add(p.resolve(strict=True))
        elif stat.S_ISREG(s.st_mode):
            found[key] = digest(p)
        elif stat.S_ISDIR(s.st_mode):
            for child in sorted(p.iterdir()):
                add(child)
        else:
            raise ValueError('selected dependency is not a regular file/directory/symlink')
    for p in paths:
        add(p)
    return found


def verify_frozen():
    check_time()
    for label, root in (('source', STATE['source']), ('harness', HARNESS)):
        require(snapshot(root) == STATE[label + '_baseline'], 'complete ' + label + ' bytes/modes changed')
    for path, expected in STATE['generated'].items():
        require(digest(path) == expected, 'generated closed input changed')
    require(closure(STATE['quick_paths']) == STATE['quick'], 'selected executable/helper/roster identity changed')


def receipt(name, argv):
    path = STATE['output'] / (name + '-owner.json')
    value = json.loads(path.read_text())
    owner = value['owner']
    require(value['status'] == 'passed_native_command_only' and value['argv'] == list(map(str, argv)) and
            value['cwd'] == str(STATE['source']) and value['expected_exit'] == value['actual_exit'] == 0 and
            value['source_sha256'] == HELPER_SHA and value['source'] == str(HARNESS / HELPER_REL),
            'original native command selection/source/exit differs: ' + name)
    require(all(value[k] is None for k in ('cleanup_error', 'cleanup_error_type', 'cancellation_received', 'cancellation_pending')) and
            type(owner['pid']) is int and owner['pid'] > 0 and owner['native_wait_calls'] == 1 and owner['start_may_begin'] is True and owner['retired'] is True and
            owner['reaped'] is True and owner['authority'] is False and owner['unknown'] is None and owner['signals'] == [] and
            owner['observed_exit'] == 0 and os.waitstatus_to_exitcode(owner['native_wait_status']) == 0 and
            owner['strongly_quarantined_until_native_reap'] is False and
            owner['post_wait_absence'] == {'pid': 'absent', 'group': 'absent'},
            'original native owner/cleanup is unconfirmed: ' + name)
    require(0 <= value['elapsed_seconds'] <= value['timeout_seconds'] <= 1200, 'original native command exceeded its declared bound')
    log = STATE['output'] / (name + '.log')
    s = log.lstat()
    exact = dict(device=s.st_dev, inode=s.st_ino, uid=s.st_uid, links=s.st_nlink,
                 bytes=s.st_size, path_matches_open_descriptor=True)
    require(stat.S_ISREG(s.st_mode) and s.st_uid == os.geteuid() and s.st_nlink == 1 and
            stat.S_IMODE(s.st_mode) == 0o600 and s.st_size <= 16 << 20 and value['capture'] == exact,
            'original capture identity differs: ' + name)
    return dict(command=name, exit=0, original_Wait_calls=1, retired_reaped_absent=True,
                receipt_sha256=digest(path), capture_sha256=digest(log))


def command(name, argv, timeout=30, network=False):
    require(name not in STATE['commands'], 'duplicate native command label')
    require(name in STATE['allowed_commands'], 'native command label is outside closed plan')
    verify_frozen()
    left = STATE['deadline'] - time.monotonic() - 20
    require(left > 0, 'native command has no remaining cleanup budget')
    env = dict(STATE['env'])
    if network:
        require(name in {'manager-selected-inputs', 'clock-selected-inputs'}, 'network outside package-specific dependency bootstrap')
        env.update(GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org')
    raw = STATE['command'].command(list(map(str, argv)), name, expected=0, env=env, timeout=min(timeout, left))
    item = receipt(name, argv)
    STATE['commands'][name] = item
    verify_frozen()
    return raw


def git(name, root, args):
    return command(name, ['/usr/bin/git', '--no-pager', '-c', 'core.fsmonitor=false',
                          '-c', 'core.hooksPath=/dev/null', '-C', root] + args)


def tree(raw):
    found = {}
    for line in raw.split('\0'):
        if not line:
            continue
        meta, name = line.split('\t', 1)
        mode, kind, obj = meta.split()
        require(kind == 'blob' and mode in {'100644', '100755', '120000'} and
                re.fullmatch('[0-9a-f]{40}', obj) and not Path(name).is_absolute() and
                '..' not in Path(name).parts and name not in found, 'Git tree is not closed')
        found[name] = dict(mode=mode, object=obj)
    return found


def bind_tree(snapshot_value, git_tree):
    require(set(snapshot_value) == set(git_tree), 'tracked roster differs from actual complete source files')
    require(all(v['mode'] == git_tree[n]['mode'] and v['git_blob_sha1'] == git_tree[n]['object'] for n, v in snapshot_value.items()),
            'source content/mode differs from immutable Git blobs')


def parse_roster(path):
    raw = json.loads(path.read_text())
    require(raw.get('schema') == 'arm.lease-clock-linux-race-roster/v1' and raw.get('baseline') == '56cb9d0a030877346a5dcd371866fb1c729e2881' and raw.get('expected_root_count') == 39 and raw.get('expected_named_node_count') == 125 and raw.get('forbidden_roots') == [S3_ROOT], 'literal roster schema/baseline/count/forbidden root differs')
    require(set(raw['packages']) == {MANAGER, CLOCK}, 'roster must select manager and leaseclock only')
    result = {}
    for package, entry in raw['packages'].items():
        roots = entry['roots']
        require(type(roots) is list and len(roots) == len(set(roots)) and
                all(type(n) is str and re.fullmatch('Test[A-Za-z0-9_]+', n) and n != S3_ROOT for n in roots),
                'root roster contains a duplicate, pattern or forbidden S3 root')
        required = entry.get('required_nodes', [])
        require(entry.get('exact_nodes') is True, 'each package must require its complete exact scenario roster')
        exact = required
        for names in (required, exact if exact is not None else []):
            require(type(names) is list and len(names) == len(set(names)) and
                    all(type(n) is str and n.split('/')[0] in roots for n in names), 'invalid subtest node roster')
        result[package] = dict(roots=roots, required_nodes=required, expected_nodes=exact)
    require(len(result[MANAGER]['roots']) == 35 and len(result[CLOCK]['roots']) == 4 and len(result[MANAGER]['required_nodes']) == 114 and len(result[CLOCK]['required_nodes']) == 11, 'exact 35+4 roots and 114+11 native nodes are required')
    require(all(set(e['roots']) <= set(e['required_nodes']) for e in result.values()), 'exact node roster must include every root')
    require({'TestNativeClockSuspendedProcess', 'TestInitialTimeNamespaceRejectsReusedOrUnverifiedScopes'} <= set(result[CLOCK]['roots']),
            'native original-child clock stop and namespace roots are required')
    require('TestLeaseClockDisposableGuestBindingGuards' in result[MANAGER]['roots'] and
            'TestSSHEndingJournalFailureCancelsOriginalGroup' in result[MANAGER]['roots'], 'actual Linux guard/ending journal roots absent')
    require({'TestLeaseClockNativeDeadlineRecoveryPreservesElapsedAuthority',
             'TestLeaseClockNativeRecoveryUncertainPersistedClockRetainsCharges',
             'TestLeaseClockNativeRecoveredPIDIsNotCancellationAuthority',
             'TestLeaseClockNativeManagerOwnerCrashRetainsRelayJoin'} <= set(result[MANAGER]['roots']), 'new native recovery/crash roots absent')
    require(set(raw.get('new_recovery_roots', [])) == {'TestLeaseClockNativeDeadlineRecoveryPreservesElapsedAuthority', 'TestLeaseClockNativeRecoveryUncertainPersistedClockRetainsCharges', 'TestLeaseClockNativeRecoveredPIDIsNotCancellationAuthority', 'TestLeaseClockNativeManagerOwnerCrashRetainsRelayJoin'}, 'literal new recovery roster differs')
    return result


def locked_sums(root):
    values = {}
    for line in (root / 'go.sum').read_text().splitlines():
        module, version, checksum = line.split()
        require(re.fullmatch(r'h1:[A-Za-z0-9+/]{43}=', checksum) and (module, version) not in values, 'go.sum input malformed/duplicate')
        values[module, version] = checksum
    return values


def package_json(raw, sums):
    decoder, result, downloads = json.JSONDecoder(), [], []
    while raw.strip():
        check_time()
        raw = raw.lstrip()
        if raw.startswith('go: downloading '):
            line, raw = raw.split('\n', 1)
            match = re.fullmatch(r'go: downloading (\S+) (\S+)', line)
            require(match and (match[1], match[2]) in sums, 'bootstrap downloaded an unlocked module')
            downloads.append(dict(module=match[1], version=match[2]))
            continue
        value, end = decoder.raw_decode(raw)
        require(type(value) is dict, 'package stream contains a non-object')
        result.append(value)
        raw = raw[end:]
    return result, downloads


def package_inputs(packages, target, sums):
    main, modules, directories, metadata = [], {}, set(), set()
    for package in packages:
        check_time()
        require(not package.get('Error') and not package.get('DepsErrors') and not package.get('Incomplete'), 'selected package graph is incomplete')
        if package.get('ImportPath') == target:
            main.append(package)
        require(not package.get('CXXFiles') and not package.get('FFiles'), 'selected C++/Fortran compilation needs a separately reviewed compiler closure')
        item = package.get('Module')
        if not item:
            continue
        if item.get('Main'):
            require(item['Path'] == 'getarm.org' and item['Dir'] == str(STATE['source']) and
                    item['GoMod'] == str(STATE['source'] / 'go.mod'), 'actual main module binding differs')
        actual = item.get('Replace') or item
        for entry in (item, item.get('Replace')):
            if not entry:
                continue
            require(not entry.get('Error'), 'selected module/replacement error')
            directory = entry.get('Dir')
            if directory:
                p = Path(directory)
                require(p.is_absolute() and p.is_dir(), 'selected module directory absent')
                resolved = p.resolve(strict=True)
                require(resolved == STATE['source'] or STATE['source'] in resolved.parents or STATE['modcache'] in resolved.parents,
                        'selected module escaped immutable source/private module cache')
                directories.add(str(resolved))
                if p.is_symlink():
                    metadata.add(str(p))
            gomod = entry.get('GoMod')
            if gomod:
                p = Path(gomod)
                require(p.is_absolute() and p.is_file(), 'selected GoMod missing')
                metadata.add(str(p))
        record = dict(path=actual['Path'], version=actual.get('Version'), directory=actual.get('Dir'),
                      go_mod=actual.get('GoMod'), replacement=item.get('Replace'))
        if actual.get('Version'):
            module, version = actual['Path'], actual['Version']
            require((module, version) in sums and (module, version + '/go.mod') in sums, 'selected module is not locked in source go.sum')
            require(not module.startswith(('.', '/')) and '..' not in Path(module).parts and '/' not in version, 'invalid selected module cache key')
            escape = lambda v: ''.join('!' + c.lower() if 'A' <= c <= 'Z' else c for c in v)
            download = STATE['modcache'] / 'cache/download' / escape(module) / '@v'
            ziphash = download / (escape(version) + '.ziphash')
            modfile = download / (escape(version) + '.mod')
            require(ziphash.is_file() and modfile.is_file() and ziphash.read_text().strip() == sums[module, version], 'actual selected zip checksum differs from locked go.sum')
            metadata.update((str(ziphash), str(modfile)))
            record.update(sum=sums[module, version], go_mod_sum=sums[module, version + '/go.mod'])
            for key, expected in (('Sum', sums[module, version]), ('GoModSum', sums[module, version + '/go.mod'])):
                require(not actual.get(key) or actual[key] == expected, 'reported selected module checksum differs')
        elif not item.get('Main'):
            p = Path(actual.get('Dir', ''))
            require(p == STATE['source'] or STATE['source'] in p.parents, 'unversioned external replacement is not in immutable source')
        key = (record['path'], record['version'], record['directory'])
        require(key not in modules or modules[key] == record, 'selected module metadata conflicts')
        modules[key] = record
    require(len(main) == 1, 'actual target package is absent/duplicate')
    selected = main[0]
    source_tests = selected.get('TestGoFiles', []) + selected.get('XTestGoFiles', [])
    declarations = []
    for filename in source_tests:
        require(Path(filename).name == filename, 'selected test filename is not simple')
        declarations += re.findall(r'^func (Test\w+)\(', (Path(selected['Dir']) / filename).read_text(), re.M)
    for root in STATE['roster'][target]['roots']:
        require(declarations.count(root) == 1, 'selected native test root is absent/duplicate in actual platform package')
    if target == MANAGER:
        require('lease_clock_suspend_guest_linux_test.go' in source_tests, 'actual Linux guest binding test source was omitted')
    return dict(target=target, packages=packages, selected_modules=list(modules.values()),
                resolved_module_directories=sorted(directories), metadata_files=sorted(metadata),
                selected_native_test_files=source_tests), directories, metadata


def compiler_path(raw, directory=False):
    name = raw.strip()
    require(name and '\n' not in name, 'compiler query returned ambiguous path')
    p = Path(name)
    if not p.is_absolute():
        located = shutil.which(name, path=STATE['env']['PATH'])
        require(located, 'compiler component did not resolve in closed PATH')
        p = Path(located)
    p = p.resolve(strict=True)
    require(p.is_dir() if directory else p.is_file(), 'queried compiler component is absent')
    return p


def parse_tests(raw):
    runs, passes, package_pass = collections.Counter(), collections.Counter(), collections.Counter()
    for line in raw.splitlines():
        check_time()
        event = json.loads(line)
        package, action, name = event.get('Package'), event['Action'], event.get('Test')
        require(package in STATE['roster'] and action not in {'skip', 'fail', 'build-fail'}, 'foreign/skipped/failed native race event')
        require(action in {'start', 'run', 'pause', 'cont', 'output', 'pass'}, 'unknown native test event')
        if action == 'output':
            require(not BAD_OUTPUT.search(event.get('Output', '')), 'panic/build/race/timeout diagnostic')
        if name is not None:
            require(name.split('/')[0] in STATE['roster'][package]['roots'] and name.split('/')[0] != S3_ROOT, 'foreign or forbidden real-S3 root executed')
            if action == 'run':
                runs[package, name] += 1
            elif action == 'pass':
                passes[package, name] += 1
        elif action == 'pass':
            package_pass[package] += 1
    require(runs == passes and runs and all(n == 1 for n in runs.values()), 'native root/subtest run-pass mismatch/duplicate')
    observed = {}
    for package, entry in STATE['roster'].items():
        names = {n for p, n in runs if p == package}
        roots = {n for n in names if '/' not in n}
        require(roots == set(entry['roots']) and package_pass[package] == 1, 'exact native root/package pass roster incomplete')
        require(set(entry['required_nodes']) <= names, 'required native scenario absent')
        if entry['expected_nodes'] is not None:
            require(names == set(entry['expected_nodes']), 'exact native scenario roster differs')
        observed[package] = sorted(names)
    guard = 'TestLeaseClockDisposableGuestBindingGuards'
    guard_nodes = {guard + '/' + n for n in ['ARM_DISPOSABLE_SUSPEND_GUEST', 'ARM_SUSPEND_NONCE', 'ARM_SUSPEND_UUID', 'ARM_SUSPEND_DISK_SERIAL', 'ARM_SUSPEND_BINARY_SHA256'] + ['marker-field-' + str(i) for i in range(5)]}
    require({n for n in observed[MANAGER] if n.startswith(guard + '/')} == guard_nodes, 'exact actual Linux ten guard cases differ')
    return dict(roots={p: sorted(v['roots']) for p, v in STATE['roster'].items()},
                observed_nodes=observed, root_count=39, test_and_subtest_nodes=len(runs),
                package_passes=2, skipped_nodes=0, failed_nodes=0)


def main(args):
    global STATE
    require(sys.platform == 'linux' and platform.machine() in {'x86_64', 'amd64'}, 'requires native Linux amd64')
    os.umask(0o077)
    deadline = time.monotonic() + 1200
    source, output, roster = args.source_root.resolve(strict=True), args.output.resolve(), args.roster.resolve(strict=True)
    require(re.fullmatch('[0-9a-f]{40}', args.expected_source), 'expected source must be an immutable full Git SHA')
    require(source != HARNESS and source.parent == HARNESS.parent, 'source A and harness B must be separate sibling checkouts')
    require(not output.exists() and not output.is_relative_to(source) and not output.is_relative_to(HARNESS), 'requires a new output directory outside both checkouts')
    work = output.with_name(output.name + '-work')
    require(not work.exists() and not work.is_symlink() and not work.is_relative_to(source) and not work.is_relative_to(HARNESS) and not work.is_relative_to(output), 'requires a fresh private sibling work directory outside artifacts and both checkouts')
    require(roster.is_relative_to(HARNESS) and roster.is_file(), 'roster must be tracked inside harness B')
    roster_rel = roster.relative_to(HARNESS).as_posix()
    require((source / roster_rel).is_file(), 'source A must already contain the exact roster')
    output.mkdir(mode=0o700, parents=True)
    work.mkdir(mode=0o700)
    for name in ('home', 'tmp', 'go-cache', 'module-cache'):
        (work / name).mkdir(mode=0o700)
    go_requested = Path(shutil.which('go') or '')
    require(go_requested.is_file(), 'setup-go selected Go binary is unavailable')
    go = go_requested.resolve(strict=True)
    goroot = go.parents[1]
    STATE = dict(source=source, output=output, work=work, deadline=deadline, commands={}, generated={},
                 modcache=work / 'module-cache',
                 quick_paths=[], quick={}, roster=parse_roster(roster))
    STATE['source_baseline'], STATE['harness_baseline'] = snapshot(source), snapshot(HARNESS)
    for name in (RUNNER_REL, HELPER_REL, roster_rel):
        require((source / name).read_bytes() == (HARNESS / name).read_bytes(), 'A/B runner/helper/roster bytes differ')
    require(digest(HARNESS / HELPER_REL) == HELPER_SHA, 'reviewed original-native helper differs')
    STATE['quick_paths'] = [SELF, HARNESS / HELPER_REL, roster, source / HELPER_REL,
                            go_requested, go, Path('/usr/bin/git'), Path('/usr/bin/gcc')]
    STATE['quick'] = closure(STATE['quick_paths'])
    STATE['allowed_commands'] = {'source-head', 'harness-head', 'source-tree', 'harness-tree', 'source-status', 'harness-status',
        'go-environment', 'cc-version', 'cc-target', 'cc-full-version', 'cc-search-dirs', 'cc-libgcc', 'cc-include', 'cc-native-headers',
        'manager-selected-inputs', 'clock-selected-inputs', 'linux-race-tests', 'source-head-final', 'harness-head-final', 'source-status-final', 'harness-status-final'} | {'cc-prog-' + n for n in COMPILER_PROGS} | {'cc-file-' + n for n in COMPILER_FILES}
    STATE['env'] = dict(PATH=str(go.parent) + ':/usr/bin:/bin', HOME=str(work / 'home'), LANG='C.UTF-8', LC_ALL='C.UTF-8', TZ='UTC',
        TMPDIR=str(work / 'tmp'), GOMAXPROCS='1', GOCACHE=str(work / 'go-cache'), GOMODCACHE=str(STATE['modcache']),
        GOTOOLCHAIN='local', GOROOT=str(goroot), GOPROXY='off', GOSUMDB='off', GOPRIVATE='', GONOPROXY='', GONOSUMDB='',
        GOINSECURE='', GOVCS='*:off', GOENV='off', GOWORK='off', GOFLAGS='', GOEXPERIMENT='', GOOS='linux', GOARCH='amd64',
        CGO_ENABLED='1', CC='/usr/bin/gcc', CXX='/usr/bin/g++', GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
        GIT_NO_REPLACE_OBJECTS='1', GIT_OPTIONAL_LOCKS='0')
    spec = importlib.util.spec_from_file_location('linux_race_original_commands', HARNESS / HELPER_REL)
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    STATE['command'] = helper.Command(output, source)
    tool_paths = None
    try:
        require(git('source-head', source, ['rev-parse', 'HEAD']).strip() == args.expected_source, 'source A immutable HEAD differs')
        heads = git('harness-head', HARNESS, ['rev-parse', 'HEAD', 'HEAD^']).splitlines()
        require(len(heads) == 2 and re.fullmatch('[0-9a-f]{40}', heads[0]) and heads[1] == args.expected_source, 'harness B is not a direct child of tested A')
        source_tree = tree(git('source-tree', source, ['ls-tree', '-r', '-z', '--full-tree', 'HEAD']))
        harness_tree = tree(git('harness-tree', HARNESS, ['ls-tree', '-r', '-z', '--full-tree', 'HEAD']))
        bind_tree(STATE['source_baseline'], source_tree)
        bind_tree(STATE['harness_baseline'], harness_tree)
        changed = {n for n in set(source_tree) | set(harness_tree) if source_tree.get(n) != harness_tree.get(n)}
        require(changed == {WORKFLOW_REL}, 'A to B must change exactly the one reviewed workflow')
        require(not git('source-status', source, ['status', '--porcelain=v1', '--untracked-files=all']).strip() and
                not git('harness-status', HARNESS, ['status', '--porcelain=v1', '--untracked-files=all']).strip(), 'A/B Git state is not clean')
        require(os.environ.get('GITHUB_ACTIONS') == 'true' and os.environ.get('GITHUB_SHA') == heads[0] and
                os.environ.get('RUNNER_OS') == 'Linux' and os.environ.get('RUNNER_ARCH') == 'X64' and
                os.environ.get('ImageOS') == 'ubuntu24', 'requires the declared GitHub Ubuntu24 hosted context bound to B')
        os_release = Path('/etc/os-release').read_text()
        require(re.search(r'^ID=ubuntu$', os_release, re.M) and re.search(r'^VERSION_ID="24\.04"$', os_release, re.M), 'native OS image differs')
        versions = re.findall(r'^go (\d+\.\d+\.\d+)$', (source / 'go.mod').read_text(), re.M)
        require(len(versions) == 1, 'source go.mod must pin one exact Go version')
        public_context = {k: os.environ.get(k) for k in ('GITHUB_REPOSITORY', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT', 'GITHUB_SHA', 'GITHUB_WORKFLOW', 'RUNNER_OS', 'RUNNER_ARCH', 'ImageOS', 'ImageVersion')}
        save('intent.json', dict(schema='arm.linux-race-recovery-intent/v1', tested_source=args.expected_source, harness=heads[0], workflow_only_child=True,
            runner_sha256=digest(SELF), helper_sha256=HELPER_SHA, roster_sha256=digest(roster), roster=STATE['roster'],
            maximum_seconds=1200, maximum_command_output_bytes=16 << 20, GOMAXPROCS=1, CGO_ENABLED=1, race=True,
            scratch_directory=str(work), scratch_directory_outside_artifacts=True, scratch_directory_uploaded=False,
            go='go' + versions[0], compiler='/usr/bin/gcc', public_context=public_context, VM_launches=0, actual_S3_cycles=0,
            host_power_operations=[], system_service_operations=[], native_clock_stop='fresh original test child only', compiler_trust_boundary=TRUST,
            expected_native_commands=sorted(STATE['allowed_commands'])))
        save('source-git-tree.json', source_tree)
        save('harness-git-tree.json', harness_tree)
        save('sources-before.json', STATE['source_baseline'])
        save('harness-before.json', STATE['harness_baseline'])
        initial_go = closure([go_requested, goroot])
        save('go-installation-before-bootstrap.json', initial_go)
        env_keys = ['GOVERSION', 'GOROOT', 'GOOS', 'GOARCH', 'GOMOD', 'GOMODCACHE', 'GOCACHE', 'CGO_ENABLED', 'CC', 'CXX', 'GOFLAGS', 'GOWORK', 'GOENV', 'GOTOOLCHAIN', 'GOEXPERIMENT']
        resolved = json.loads(command('go-environment', [go, 'env', '-json'] + env_keys))
        exact = dict(GOVERSION='go' + versions[0], GOROOT=str(goroot), GOOS='linux', GOARCH='amd64', GOMOD=str(source / 'go.mod'),
            GOMODCACHE=str(STATE['modcache']), GOCACHE=str(work / 'go-cache'), CGO_ENABLED='1', CC='/usr/bin/gcc', CXX='/usr/bin/g++',
            GOFLAGS='', GOWORK='off', GOENV='', GOTOOLCHAIN='local', GOEXPERIMENT='')
        require(resolved == exact, 'resolved native race Go environment differs')
        save('go-environment.json', resolved)
        cc = Path('/usr/bin/gcc')
        compiler_queries = {'version': command('cc-version', [cc, '--version']),
            'target': command('cc-target', [cc, '-dumpmachine']).strip(),
            'full_version': command('cc-full-version', [cc, '-dumpfullversion', '-dumpversion']).strip(),
            'search_dirs': command('cc-search-dirs', [cc, '-print-search-dirs'])}
        require(re.fullmatch(r'x86_64[-\w]*linux[-\w]*', compiler_queries['target']), 'GCC does not target native Linux amd64')
        components = [cc]
        for name in COMPILER_PROGS:
            components.append(compiler_path(command('cc-prog-' + name, [cc, '-print-prog-name=' + name])))
        components.append(compiler_path(command('cc-libgcc', [cc, '-print-libgcc-file-name'])))
        components.append(compiler_path(command('cc-include', [cc, '-print-file-name=include']), directory=True))
        for name in COMPILER_FILES:
            components.append(compiler_path(command('cc-file-' + name, [cc, '-print-file-name=' + name])))
        compiler_before_headers = closure(components)
        save('compiler-before-header-probe.json', compiler_before_headers)
        probe = output / 'native-header-probe.c'
        probe.write_text('/* Fixed native cgo/race header-identity probe; no code executes. */\n')
        STATE['generated'][probe] = digest(probe)
        header_argv = [cc, '-M', '-x', 'c', '-pthread', '-D_GNU_SOURCE', '-MT', 'arm_native_headers']
        for name in HEADER_NAMES:
            header_argv += ['-include', name]
        header_raw = command('cc-native-headers', header_argv + [probe])
        target, body = header_raw.replace('\\\n', ' ').split(':', 1)
        require(target.strip() == 'arm_native_headers', 'native header probe target differs')
        headers = [Path(p) for p in shlex.split(body)]
        require(headers and probe in headers and all(p.is_absolute() and p.is_file() for p in headers), 'actual native header dependency closure is incomplete')
        sums = locked_sums(source)
        directories, metadata, selected_modules = set(), set(), {}
        for name, target, arg in (('manager', MANAGER, './internal/manager'), ('clock', CLOCK, './internal/leaseclock')):
            raw = command(name + '-selected-inputs', [go, 'list', '-mod=readonly', '-deps', '-test', '-race', '-json', arg], timeout=240, network=True)
            packages, downloads = package_json(raw, sums)
            selection, dirs, files = package_inputs(packages, target, sums)
            selection['bootstrap_downloads'] = downloads
            save(name + '-package-selection.json', selection)
            directories.update(dirs)
            metadata.update(files)
            selected_modules[name] = selection['selected_modules']
        require(closure([go_requested, goroot]) == initial_go and closure(components) == compiler_before_headers, 'Go/compiler changed during bootstrap')
        external_dirs = [Path(p) for p in sorted(directories) if not (Path(p) == source or source in Path(p).parents)]
        tool_paths = [go_requested, goroot, Path('/usr/bin/git'), Path(sys.executable)] + components + headers + external_dirs + [Path(p) for p in sorted(metadata)]
        selected = closure(tool_paths)
        save('compiler-selection.json', dict(queries=compiler_queries, resolved_components=list(map(str, components)), native_header_family=HEADER_NAMES,
            native_header_dependencies=list(map(str, headers)), trust_boundary=TRUST))
        save('module-selection.json', dict(selected_modules=selected_modules, resolved_directories=sorted(directories),
            checksum_metadata_files=sorted(metadata), graph_all_requested=False, unrelated_module_cache_hashed=False,
            bootstrap_proxy='https://proxy.golang.org', bootstrap_sumdb='sum.golang.org', subsequent_module_network='off'))
        save('tools-before.json', selected)
        pattern = '^(' + '|'.join(sorted({n for e in STATE['roster'].values() for n in e['roots']})) + ')$'
        require(S3_ROOT not in pattern, 'forbidden real-suspend root in test selector')
        raw = command('linux-race-tests', [go, 'test', '-mod=readonly', '-race', '-json', '-count=1', '-p=1', '-parallel=1',
            '-timeout=300s', '-run', pattern, './internal/manager', './internal/leaseclock'], timeout=900)
        observed = parse_tests(raw)
        save('test-roster.json', observed)
        require(git('source-head-final', source, ['rev-parse', 'HEAD']).strip() == args.expected_source and
                git('harness-head-final', HARNESS, ['rev-parse', 'HEAD']).strip() == heads[0], 'immutable A/B HEAD changed')
        require(not git('source-status-final', source, ['status', '--porcelain=v1', '--untracked-files=all']).strip() and
                not git('harness-status-final', HARNESS, ['status', '--porcelain=v1', '--untracked-files=all']).strip(), 'A/B final Git state is not clean')
        verify_frozen()
        final_tools = closure(tool_paths)
        save('sources-after.json', snapshot(source))
        save('harness-after.json', snapshot(HARNESS))
        save('tools-after.json', final_tools)
        require(final_tools == selected and set(STATE['commands']) == STATE['allowed_commands'], 'final selected closure/closed command roster differs')
        check_time()
        save('result.json', dict(schema='arm.linux-race-recovery-result/v1', status='passed_native_linux_race_recovery',
            tested_source=args.expected_source, harness=heads[0], source_files=len(STATE['source_baseline']), harness_files=len(STATE['harness_baseline']),
            source_before_after_equal=True, harness_before_after_equal=True, selected_tool_entries=len(selected), tools_before_after_equal=True,
            runner_sha256=digest(SELF), helper_sha256=HELPER_SHA, roster_sha256=digest(roster), test_roster=observed,
            commands=list(STATE['commands'].values()), original_native_Wait_calls=len(STATE['commands']), all_original_owners_retired_reaped_absent=True,
            source_manifest_sha256=hashlib.sha256(json.dumps(STATE['source_baseline'], sort_keys=True).encode()).hexdigest(),
            harness_manifest_sha256=hashlib.sha256(json.dumps(STATE['harness_baseline'], sort_keys=True).encode()).hexdigest(),
            tool_manifest_sha256=hashlib.sha256(json.dumps(selected, sort_keys=True).encode()).hexdigest(),
            VM_launches=0, actual_S3_cycles=0, host_power_operations=[], system_service_operations=[], compiler_trust_boundary=TRUST,
            scratch_directory_outside_artifacts=True, scratch_directory_uploaded=False,
            scope='Exact native Linux amd64 CGO1 race-instrumented manager/recovery and leaseclock cohort; fresh original-child stop test only. No VM, host suspend, external SSH, full-suite or full-specification claim.'))
        verify_frozen()
        check_time()
        print('PASS native Linux CGO1 race: exact35 manager+4 clock roots; no VM or power operation', flush=True)
    except BaseException as error:
        captures = {}
        for name, producer in (('sources-on-failure.json', lambda: snapshot(source)), ('harness-on-failure.json', lambda: snapshot(HARNESS)),
                               ('tools-on-failure.json', lambda: closure(tool_paths) if tool_paths is not None else {})):
            try:
                check_time()
                save(name, producer())
                captures[name] = 'retained'
            except BaseException as failure:
                captures[name] = type(failure).__name__ + ': ' + str(failure)[:1024]
        save('result.json', dict(status='failed', tested_source=args.expected_source, runner_sha256=digest(SELF) if time.monotonic() < deadline else None,
            completed_commands=list(STATE['commands']), failure_captures=captures, error_type=type(error).__name__, error=str(error)[:4096]))
        raise


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', type=Path, required=True)
    parser.add_argument('--expected-source', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--roster', type=Path, required=True)
    main(parser.parse_args())
