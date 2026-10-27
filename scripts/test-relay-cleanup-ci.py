"""Scoped relay cleanup CI; import-safe readers do not execute development tools."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
ROSTER = {
    'relay_cleanup_test.go': {
        'TestRelayCleanupUncertainRetainsOpenDescriptorThroughCloseAndRestart',
        'TestRelayCleanupUncertainRejectsForgedJoinedAndSettledHandles',
        'TestRelayCleanupUncertainChangedBindingIsStickyWithoutOverwritingRow',
        'TestRelayCleanupUncertainWriteFailureCannotBecomeTerminal',
        'TestRelayCleanupCloseCancelsBeforeUnreadableLedger',
        'TestRelayCleanupUncertainAfterCancellationStillRequiresOriginalJoin',
    },
    'relay_transit_test.go': {
        'TestRelayTransitReleaseRequiresOriginalJoin',
        'TestRelayTransitAutomaticClosureDoesNotClaimExecutionCompleted',
        'TestRelayTransitExpiryAndForceStopKeepBlockedJoinCharged',
        'TestRelayTransitExactOwnerAndPublicMarkerDenial',
        'TestRelayTransitAdmissionFailureStartsNoOwner',
        'TestRelayTransitChangedDurableBindingCannotSettle',
        'TestRelayTransitJoinPersistenceFailureRetainsCharge',
        'TestRelayTransitEndingWriteFailureStillCancelsAndRetainsCharge',
        'TestRelayTransitCloseWaitsWithoutHoldingManagerMutex',
        'TestRelayTransitCloseCancelsEvenWhenEndingWriteFails',
        'TestRelayTransitRecoveredMarkerRemainsChargedAndBlocksReset',
        'TestRelayTransitSettledMarkerAllowsResetProof',
        'TestForwardTransitSettledMarkerAllowsResetProof',
    },
}


def require(value, message):
    if not value:
        raise ValueError(message)


def sources(root):
    result = {}
    for path in root.rglob('*'):
        if path.relative_to(root).parts[0] in ('.git', '.cache', '.tools', '.tla', '.ci-evidence', 'bin', 'dist'):
            continue
        require(not path.is_symlink(), 'source symlink refused: ' + str(path.relative_to(root)))
        if path.is_file():
            data = path.read_bytes()
        else:
            continue
        result[str(path.relative_to(root))] = hashlib.sha256(data).hexdigest()
    return result


def assert_committed(root, source):
    subprocess.run(['git', 'diff', '--quiet', 'HEAD', '--'], cwd=root, check=True)
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    require(head == os.environ['GITHUB_SHA'], 'checkout differs from dispatched commit')
    for name, digest in source.items():
        blob = subprocess.check_output(['git', 'show', head + ':' + name], cwd=root)
        require(hashlib.sha256(blob).hexdigest() == digest, 'source differs from commit: ' + name)
    return head


def closure(paths):
    result = {}

    def visit(path, ancestors):
        if path.is_symlink():
            result[str(path) + ':link'] = os.readlink(path)
        resolved = path.resolve(strict=True)
        if resolved.is_dir():
            require(resolved not in ancestors, 'cyclic selected dependency directory')
            for child in sorted(path.iterdir()):
                visit(child, ancestors | {resolved})
        else:
            require(resolved.is_file(), 'selected dependency is not a regular file')
            result[str(path)] = hashlib.sha256(path.read_bytes()).hexdigest()

    for value in paths:
        visit(Path(value), set())
    return result


def expected_roster(root):
    declaration = (root / 'go.mod').read_text().splitlines()[0]
    match = re.fullmatch(r'module ([A-Za-z0-9._/-]+)', declaration)
    require(match is not None, 'exact module declaration required')
    names = set()
    for filename, expected in ROSTER.items():
        actual = re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing.T\)', (root / 'internal/manager' / filename).read_text(), re.M)
        require(len(actual) == len(expected) and set(actual) == expected, 'declared relay source roster changed: ' + filename)
        require(not names.intersection(expected), 'duplicate relay root test')
        names.update(expected)
    return {match[1] + '/internal/manager': names}


def completed_roster(raw, expected):
    ran = {key: [] for key in expected}
    passed = {key: [] for key in expected}
    packages = []
    observed_run, observed_pass = {}, {}
    for line in raw.splitlines():
        event = json.loads(line)
        package, action = event['Package'], event['Action']
        require(package in expected and action in ('start', 'run', 'pause', 'cont', 'pass', 'bench', 'output'), 'failed/skipped/foreign/unknown relay test event')
        if 'Test' in event:
            name = event['Test']
            require(name.split('/')[0] in expected[package], 'unexpected relay root test')
            key = (package, name)
            if action == 'run':
                observed_run[key] = observed_run.get(key, 0) + 1
                if '/' not in name:
                    ran[package].append(name)
            elif action == 'pass':
                observed_pass[key] = observed_pass.get(key, 0) + 1
                if '/' not in name:
                    passed[package].append(name)
        elif action == 'pass':
            packages.append(package)
    require(observed_run == observed_pass and all(count == 1 for count in observed_run.values()), 'observed relay test/subtest completion differs')
    for package, names in expected.items():
        require(set(ran[package]) == names and set(passed[package]) == names and len(ran[package]) == len(names) and len(passed[package]) == len(names) and packages.count(package) == 1, 'incomplete exact relay root roster')
    return passed, sorted(name for (_, name) in observed_run)


def write_json(path, value):
    path.write_text(json.dumps(value, sort_keys=True, indent=2) + '\n')


def main():
    require(os.environ.get('GITHUB_ACTIONS') == 'true', 'explicit remote CI execution required')
    os.umask(0o077)
    run = ROOT / '.tla/relay-cleanup-native'
    run.mkdir(mode=0o700, parents=True, exist_ok=False)
    before = sources(ROOT)
    initial_tools, selected, closure_paths, tool_paths = {}, None, [], []
    failure, final_failures = None, []
    result = {'status': 'failed', 'scope': 'six relay cleanup API roots and thirteen existing relay transit roots; complete observed subtests; not external relay integration'}
    try:
        head = assert_committed(ROOT, before)
        write_json(run / 'dispatch.json', {'commit': head, 'platform': {'system': platform.system(), 'machine': platform.machine()}, 'image': {key: os.environ.get(key) for key in ('ImageOS', 'ImageVersion', 'RUNNER_OS', 'RUNNER_ARCH')}})
        write_json(run / 'sources-before.json', before)
        expected = expected_roster(ROOT)
        write_json(run / 'expected-roster.json', {key: sorted(value) for key, value in expected.items()})
        helper = ROOT / 'scripts/formal_process.py'
        spec = importlib.util.spec_from_file_location('relay_cleanup_native_commands', helper)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        command = module.Command(run, ROOT)
        go = str(Path(shutil.which('go')).resolve(strict=True))
        env = {key: value for key, value in os.environ.items() if not key.startswith('ARM_')}
        for key in ('GOFLAGS', 'GOWORK', 'GOENV', 'GOTOOLCHAIN', 'GOEXPERIMENT', 'GOOS', 'GOARCH', 'GODEBUG', 'CGO_CFLAGS', 'CGO_CPPFLAGS', 'CGO_CXXFLAGS', 'CGO_LDFLAGS', 'GOTMPDIR', 'GOVCS', 'GO111MODULE', 'GOROOT', 'GOTOOLDIR', 'AR', 'CC', 'CXX', 'SDKROOT'):
            env.pop(key, None)
        env.update(GOMAXPROCS='1', GOCACHE=str(ROOT / '.cache/relay-cleanup-go-build'), GOPROXY='off', GOSUMDB='off', GOTOOLCHAIN='local', GOENV='off', GOWORK='off', GOFLAGS='', CGO_ENABLED='1')
        sdk = None
        if sys.platform == 'darwin':
            xcrun = str(Path(shutil.which('xcrun')).resolve(strict=True))
            tool_paths.extend([go, xcrun])
            initial_tools = closure(tool_paths)
            cc = command.command([xcrun, '--find', 'clang'], 'select-cc', env=env, timeout=30).strip()
            cxx = command.command([xcrun, '--find', 'clang++'], 'select-cxx', env=env, timeout=30).strip()
            sdk = command.command([xcrun, '--show-sdk-path'], 'select-sdk', env=env, timeout=30).strip()
        else:
            require(sys.platform.startswith('linux'), 'supported native CI platform required')
            cc = shutil.which('clang') or shutil.which('cc')
            cxx = shutil.which('clang++') or shutil.which('c++')
        require(cc and cxx, 'preinstalled C/C++ compilers required')
        env.update(CC=cc, CXX=cxx)
        if sdk:
            env.update(SDKROOT=sdk, CGO_CFLAGS='-isysroot ' + sdk, CGO_CXXFLAGS='-isysroot ' + sdk, CGO_LDFLAGS='-isysroot ' + sdk)
        tool_paths.extend([go, cc, cxx, helper])
        initial_tools = closure(tool_paths)
        write_json(run / 'tools.json', initial_tools)
        keys = ('GOVERSION', 'GOROOT', 'GOOS', 'GOARCH', 'GOFLAGS', 'GOWORK', 'GOENV', 'GOMOD', 'CC', 'CXX', 'GOMODCACHE', 'GOPATH', 'GOCACHE', 'GOPROXY', 'GOSUMDB', 'GOTOOLCHAIN', 'CGO_ENABLED', 'GOEXPERIMENT')
        resolved = json.loads(command.command([go, 'env', '-json', *keys], 'resolved-go-env', env=env, timeout=30))
        require(set(resolved) == set(keys), 'exact resolved Go environment required')
        require(resolved['GOVERSION'] == 'go' + re.search(r'^go (\S+)$', (ROOT / 'go.mod').read_text(), re.M)[1] and resolved['GOOS'] == ('darwin' if sdk else 'linux') and resolved['GOARCH'] == 'arm64' and resolved['GOENV'] == '' and resolved['GOMOD'] == str(ROOT / 'go.mod') and resolved['GOEXPERIMENT'] == '', 'unexpected Go source/tool selection')
        for key in ('GOFLAGS', 'GOWORK', 'CC', 'CXX', 'GOCACHE', 'GOPROXY', 'GOSUMDB', 'GOTOOLCHAIN', 'CGO_ENABLED'):
            require(resolved[key] == env[key], 'resolved Go selector differs: ' + key)
        closure_paths.extend([resolved['GOROOT'], resolved['GOMODCACHE'], go, cc, cxx, helper])
        for name, compiler in (('cc', cc), ('cxx', cxx)):
            resource = command.command([compiler, '-print-resource-dir'], name + '-resource', env=env, timeout=30).strip()
            linker = command.command([compiler, '-print-prog-name=ld'], name + '-linker', env=env, timeout=30).strip()
            if not os.path.isabs(linker):
                linker = shutil.which(linker)
            require(resource and linker, 'selected compiler resources/linker required')
            closure_paths.extend([resource, linker])
        if sdk:
            closure_paths.extend([str(Path(sdk) / 'usr/include'), str(Path(sdk) / 'usr/lib')])
        write_json(run / 'selected-cgo.json', {key: env.get(key) for key in ('CC', 'CXX', 'SDKROOT', 'CGO_CFLAGS', 'CGO_CXXFLAGS', 'CGO_LDFLAGS')})
        selected = closure(closure_paths)
        write_json(run / 'tool-dependency-closure-before.json', selected)
        command.command([go, 'test', '-p=1', '-mod=readonly', '-buildvcs=false', '-race', '-run', '^$', './internal/manager'], 'affected-compile', env=env, timeout=360)
        pattern = '^(' + '|'.join(sorted(set.union(*expected.values()))) + ')$'
        raw = command.command([go, 'test', '-p=1', '-mod=readonly', '-buildvcs=false', '-json', '-race', '-count=1', '-timeout=180s', '-run', pattern, './internal/manager'], 'relay-cleanup-race', env=env, timeout=360)
        passed, observed = completed_roster(raw, expected)
        command.command([go, 'vet', '-p=1', '-mod=readonly', './internal/manager'], 'vet', env=env, timeout=180)
        result.update(status='passed_scoped_relay_cleanup_and_vet', roster=passed, observed_tests=observed, go_env=resolved, sources_sha256=hashlib.sha256(json.dumps(before, sort_keys=True).encode()).hexdigest())
    except BaseException as error:
        failure = error
        result.update(status='failed', error=str(error), error_type=type(error).__name__)
    finally:
        try:
            after = sources(ROOT)
            write_json(run / 'sources-after.json', after)
            assert_committed(ROOT, after)
            require(after == before, 'source changed during relay commands')
            result['source_closure'] = 'passed'
        except BaseException as error:
            final_failures.append('source: ' + str(error))
        try:
            require(closure(tool_paths) == initial_tools, 'selected tool changed during commands')
            result['tools_closure'] = 'passed'
        except BaseException as error:
            final_failures.append('tools: ' + str(error))
        try:
            if selected is not None:
                final_selected = closure(closure_paths)
                write_json(run / 'tool-dependency-closure-after.json', final_selected)
                require(selected == final_selected, 'selected Go/compiler/dependency closure changed')
                result['dependency_closure'] = 'passed'
        except BaseException as error:
            final_failures.append('dependency: ' + str(error))
        if final_failures:
            result.update(status='failed', final_closure_failures=final_failures)
        write_json(run / 'result.json', result)
    if failure is not None:
        raise failure
    require(not final_failures, 'final relay source/tool closure failed')
    print('passed exact relay cleanup/transit roster and manager vet', flush=True)


if __name__ == '__main__':
    main()
