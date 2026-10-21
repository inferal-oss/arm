#!/usr/bin/env python3
"""Offline acceptance against real Cargo/rustc, Gradle/JDK and uv/Python.

No tool downloads, external packages, global configuration edits or mock tool
executables. Missing prerequisites fail by default; --allow-missing is explicit.
Failed fixtures are retained when owned processes or authority shutdown cannot
be verified. Run on Linux in an observable process namespace for cleanup tests.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile


class Fixture:
    def __init__(self, binary, root, env):
        self.root, self.env = root, env
        self.home = root / 'state'
        self.prefix = [binary, '--home', str(self.home)]
        self.children = []
        self.stores = []

    def cli(self, *args):
        result = subprocess.run(self.prefix + list(args), env=self.env,
                                capture_output=True, text=True, timeout=30)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return json.loads(result.stdout)

    def storage(self, name):
        lease = self.cli('storage', 'create', '512MiB', '--ttl', '10m',
                         '--reason', name + ' real-tool build artifacts')
        self.stores.append(lease['id'])
        return lease

    def launch(self, name, project, store, command, profile=None, ttl='2m'):
        args = ['run', '--cpu', '1', '--storage', store['id'], '--ttl', ttl,
                '--cwd', str(project), '--reason', name, '--json']
        if profile:
            args += ['--profile', profile]
        # Files rather than unread pipes: verbose real compilers cannot block
        # while the test polls readiness or inspects the running lease.
        out = (self.root / (name + '.json')).open('w+')
        err = (self.root / (name + '.log')).open('w+')
        process = subprocess.Popen(self.prefix + args + ['--'] + command,
                                   env=self.env, stdout=out, stderr=err, text=True)
        handle = (process, out, err, name)
        self.children.append(handle)
        return handle

    def finish(self, handle, expected=0):
        process, out, err, name = handle
        process.wait(timeout=90)
        out.seek(0)
        err.seek(0)
        stdout, stderr = out.read(), err.read()
        assert process.returncode == expected, (name, process.returncode, stderr[-12000:], stdout)
        lease = json.loads(stdout)
        assert lease['state'] == 'terminal', (name, lease)
        assert self.cli('inspect', lease['request']['storage_lease'])['state'] == 'active'
        assert self.cli('status')['reserved']['cpu'] == 0, 'execution CPU promise leaked'
        return lease, stderr

    def cancel(self, handle, marker):
        process, _, err, name = handle
        deadline = time.monotonic() + 60
        while not marker.exists():
            if process.poll() is not None:
                err.seek(0)
                raise AssertionError((name, 'exited before readiness', err.read()))
            assert time.monotonic() < deadline, (name, 'readiness timed out')
            time.sleep(.1)
        lease = next(l for l in self.cli('status')['leases'] if l['request']['reason'] == name)
        assert lease['state'] == 'active' and lease.get('process_identity'), lease
        # The ready marker comes from a real build script/task/Python command.
        self.cli('cancel', lease['id'])
        ended, _ = self.finish(handle, 125)
        assert ended['outcome'] == 'cancelled', ended
        try:
            os.killpg(lease['process_identity']['pgid'], 0)
        except ProcessLookupError:
            pass
        else:
            raise AssertionError((name, 'supervised process group survived cancellation'))
        assert not marker.with_suffix('.late').exists(), name

    def cleanup(self, store, exported, verify):
        directory = Path(store['managed_dir'])
        digest = hashlib.sha256(exported.read_bytes()).digest()
        # A no-daemon Gradle single-use JVM can take a moment to close its
        # files after the launcher exits. Observation must confirm quiescence;
        # never treat a successful build exit as permission to erase open data.
        deadline = time.monotonic() + 30
        while True:
            preview = self.cli('storage', 'cleanup', store['id'], '--dry-run')
            if preview['can_clean'] or time.monotonic() >= deadline:
                break
            time.sleep(.25)
        assert preview['can_clean'], preview
        assert directory.exists() and exported.exists(), 'preview deleted output'
        self.cli('release', store['id'])
        deadline = time.monotonic() + 45
        while True:
            lease = self.cli('inspect', store['id'])
            if lease.get('artifact', {}).get('namespace_removed'):
                break
            assert lease['state'] != 'needs_attention', lease
            assert time.monotonic() < deadline, lease
            time.sleep(.1)
        assert not directory.exists(), 'temporary build tree survived release'
        # Namespace removal does not prove exclusive physical reclamation on
        # APFS/reflink/snapshot filesystems. Preserve ARM's honest accounting.
        if lease['state'] != 'terminal':
            assert lease['state'] == 'needs_attention' and lease['artifact']['last_error'], lease
            assert self.cli('status')['reserved']['disk_bytes'] >= 512 << 20
            self.cli('storage', 'handoff', store['id'])
        assert hashlib.sha256(exported.read_bytes()).digest() == digest
        verify()

    def close(self):
        # Never delete state under an unresolved backend or a running compiler.
        try:
            leases = self.cli('status')['leases']
            for lease in leases:
                if lease['state'] != 'terminal' and lease['request'].get('kind') != 'storage':
                    self.cli('cancel', lease['id'])
            for handle in self.children:
                if handle[0].poll() is None:
                    handle[0].wait(timeout=45)
            unresolved = [l for l in self.cli('status')['leases'] if l['state'] != 'terminal'
                          and l['request'].get('kind') != 'storage']
            assert not unresolved, ('execution ownership unresolved', unresolved)
            for sid in self.stores:
                lease = self.cli('inspect', sid)
                if lease['state'] != 'terminal':
                    if Path(lease['managed_dir']).exists():
                        preview = self.cli('storage', 'cleanup', sid, '--dry-run')
                        assert preview['can_clean'], ('storage still in use; preserve fixture', preview)
                    # Handoff is deliberate fixture disposal, never evidence
                    # that ARM successfully reclaimed this storage.
                    self.cli('storage', 'handoff', sid)
            self.cli('stop')
        except (AssertionError, subprocess.TimeoutExpired, OSError) as error:
            print('Retained unresolved real-tool fixture:', self.root, error, file=sys.stderr)
            raise RuntimeError('real-tool fixture shutdown was not verified') from error
        finally:
            for process, out, err, _ in self.children:
                if process.poll() is not None:
                    out.close()
                    err.close()
        with (self.home / 'daemon.lock').open('r+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            assert not (self.home / 'endpoint.json').exists(), ('unresolved endpoint; fixture retained', self.root)
            shutil.rmtree(self.root)


def checked(command, env):
    result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
    assert result.returncode == 0, (command, result.stderr)
    return result.stdout.strip()


def cargo_scenarios(f, tools):
    project = f.root / 'rust project'
    (project / 'src').mkdir(parents=True)
    (project / 'Cargo.toml').write_text('[package]\nname="arm_real_rust"\nversion="0.1.0"\nedition="2021"\n')
    source = project / 'src/main.rs'
    source.write_text('fn main() { println!("arm-real-rust"); }\n')
    (project / 'build.rs').write_text('''fn main() {
        assert_eq!(std::env::var("CARGO_BUILD_JOBS").unwrap(), "1");
        assert_eq!(std::env::var("NUM_JOBS").unwrap(), "1");
        let root = std::env::var("ARM_STORAGE_DIR").unwrap();
        assert!(std::env::var("OUT_DIR").unwrap().starts_with(&root));
        println!("cargo:rerun-if-changed=slow");
        if std::path::Path::new("slow").exists() {
            std::fs::write(format!("{root}/cargo.ready"), "ready").unwrap();
            std::thread::sleep(std::time::Duration::from_secs(120));
            std::fs::write(format!("{root}/cargo.late"), "escaped").unwrap();
        }
    }\n''')
    store = f.storage('cargo')
    command = [tools['cargo'], 'build', '--offline']
    lease, _ = f.finish(f.launch('cargo-success', project, store, command, 'rust-build'))
    assert lease['exit_code'] == 0
    for override in (['--jobs=2'], ['--target-dir', str(project / 'unowned-target')]):
        rejected = subprocess.run(f.prefix + ['run', '--cpu', '1', '--storage', store['id'],
            '--ttl', '1m', '--cwd', str(project), '--profile', 'rust-build',
            '--reason', 'reject cargo override', '--json', '--'] + command + override,
            env=f.env, capture_output=True, text=True, timeout=30)
        assert rejected.returncode != 0 and 'profile_conflict' in rejected.stderr, rejected.stderr
    assert not (project / 'unowned-target').exists()
    binary = Path(store['managed_dir']) / 'target/debug/arm_real_rust'
    assert binary.is_file() and not (project / 'target').exists()
    exported = f.root / 'exports/rust-app'
    shutil.copy2(binary, exported)
    verify = lambda: assert_output([str(exported)], 'arm-real-rust', f.env)
    verify()
    source.write_text('fn main() { this is not valid Rust; }\n')
    failed, log = f.finish(f.launch('cargo-failure', project, store, command, 'rust-build'), 101)
    assert failed['exit_code'] == 101 and 'error' in log.lower()
    source.write_text('fn main() { println!("arm-real-rust"); }\n')
    (project / 'slow').touch()
    f.cancel(f.launch('cargo-cancel', project, store, command, 'rust-build'),
             Path(store['managed_dir']) / 'cargo.ready')
    f.cleanup(store, exported, verify)
    print('PASS Cargo/rustc: real compilation, jobs=1, owned target, compile error/101, build-script cancellation, exported executable and cleanup', flush=True)


def gradle_scenarios(f, tools):
    project = f.root / 'gradle project'
    (project / 'src/main/java').mkdir(parents=True)
    (project / 'settings.gradle').write_text("rootProject.name = 'arm-real-java'\n")
    source = project / 'src/main/java/Main.java'
    source.write_text('public class Main { public static void main(String[] args) { System.out.println("arm-real-gradle"); } }\n')
    (project / 'build.gradle').write_text('''plugins { id 'java' }
def owned = System.getenv('ARM_STORAGE_DIR')
assert owned != null
assert gradle.startParameter.maxWorkerCount == 1
assert !gradle.startParameter.parallelProjectExecutionEnabled
layout.buildDirectory.set(file(owned + '/build'))
tasks.named('jar') { manifest { attributes('Main-Class': 'Main') } }
tasks.register('slow') {
    doLast {
        file(owned + '/gradle.ready').text = 'ready'
        Thread.sleep(120000)
        file(owned + '/gradle.late').text = 'escaped'
    }
}
''')
    store = f.storage('gradle')
    directory = Path(store['managed_dir'])
    command = [tools['gradle'], '--offline', '--no-daemon', '--no-parallel',
               '--max-workers=1', '--console=plain', '-g', str(directory / 'gradle-home'),
               '--project-cache-dir', str(directory / 'project-cache'),
               '-Dorg.gradle.jvmargs=-Xmx256m -XX:MaxMetaspaceSize=256m']
    f.finish(f.launch('gradle-success', project, store, command + ['jar']))
    assert not (project / 'build').exists() and not (project / '.gradle').exists()
    exported = f.root / 'exports/java-app.jar'
    shutil.copy2(directory / 'build/libs/arm-real-java.jar', exported)
    verify = lambda: assert_output([tools['java'], '-jar', str(exported)], 'arm-real-gradle', f.env)
    verify()
    source.write_text('public class Main { invalid Java source }\n')
    failed, log = f.finish(f.launch('gradle-failure', project, store, command + ['compileJava']), 1)
    assert failed['exit_code'] == 1 and 'Compilation failed' in log, log
    f.cancel(f.launch('gradle-cancel', project, store, command + ['slow']), directory / 'gradle.ready')
    f.cleanup(store, exported, verify)
    print('PASS Gradle/JDK: offline javac/JAR, workers=1, compile error/1, task/JVM cancellation, exported JAR and cleanup', flush=True)


def assert_output(command, expected, env):
    assert checked(command, env) == expected, command


def uv_scenarios(f, tools):
    project = f.root / 'uv project'
    project.mkdir()
    # A real, entirely local wheel exercises uv's resolver and installer without
    # relying on PyPI, an existing cache, a build backend or a network service.
    wheel = project / 'arm_fixture-0.1.0-py3-none-any.whl'
    module = 'def message():\n    return "arm-real-uv"\n'
    with zipfile.ZipFile(wheel, 'w') as archive:
        files = {'arm_fixture.py': module,
                 'arm_fixture-0.1.0.dist-info/METADATA': 'Metadata-Version: 2.1\nName: arm-fixture\nVersion: 0.1.0\n',
                 'arm_fixture-0.1.0.dist-info/WHEEL': 'Wheel-Version: 1.0\nGenerator: arm-acceptance\nRoot-Is-Purelib: true\nTag: py3-none-any\n'}
        for name, body in files.items():
            archive.writestr(name, body)
        archive.writestr('arm_fixture-0.1.0.dist-info/RECORD', ''.join(name + ',,\n' for name in files) + 'arm_fixture-0.1.0.dist-info/RECORD,,\n')
    (project / 'pyproject.toml').write_text('''[project]
name = "arm-real-uv"
version = "0.1.0"
requires-python = ">=3.9"
dependencies = ["arm-fixture==0.1.0"]
[tool.uv.sources]
arm-fixture = { path = "arm_fixture-0.1.0-py3-none-any.whl" }
''')
    (project / 'success.py').write_text('''import arm_fixture, os, pathlib, sys, zipapp
root = pathlib.Path(os.environ['ARM_STORAGE_DIR'])
assert pathlib.Path(sys.prefix).is_relative_to(root)
assert pathlib.Path(arm_fixture.__file__).is_relative_to(root)
app = root / 'app'
app.mkdir()
(app / '__main__.py').write_text('from arm_fixture import message; print(message())\\n')
(app / 'arm_fixture.py').write_text(pathlib.Path(arm_fixture.__file__).read_text())
zipapp.create_archive(app, root / 'result.pyz')
print(arm_fixture.message())
''')
    (project / 'failure.py').write_text('import arm_fixture\nraise SystemExit(7)\n')
    (project / 'slow.py').write_text('''import arm_fixture, os, pathlib, subprocess, sys, time
root = pathlib.Path(os.environ['ARM_STORAGE_DIR'])
# A real Python descendant tests group cleanup through uv -> Python -> Python.
child = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(120)'])
(root / 'uv.ready').write_text(str(child.pid))
time.sleep(120)
(root / 'uv.late').write_text('escaped')
''')
    store = f.storage('uv')
    directory = Path(store['managed_dir'])
    command = ['/usr/bin/env', 'UV_CACHE_DIR=' + str(directory / 'uv-cache'),
               'UV_PROJECT_ENVIRONMENT=' + str(directory / 'venv'),
               tools['uv'], 'run', '--offline', '--no-python-downloads',
               '--no-managed-python', '--python', tools['python'], 'python']
    _, log = f.finish(f.launch('uv-success', project, store, command + ['success.py']))
    assert 'arm-real-uv' in log and not (project / '.venv').exists()
    exported = f.root / 'exports/python-app.pyz'
    shutil.copy2(directory / 'result.pyz', exported)
    verify = lambda: assert_output([tools['python'], '-I', str(exported)], 'arm-real-uv', f.env)
    verify()
    failed, _ = f.finish(f.launch('uv-failure', project, store, command + ['failure.py']), 7)
    assert failed['exit_code'] == 7
    f.cancel(f.launch('uv-cancel', project, store, command + ['slow.py']), directory / 'uv.ready')
    # Also prove that a real uv workload cannot outlive its execution deadline.
    (directory / 'uv.ready').unlink()
    expired, _ = f.finish(f.launch('uv-expiry', project, store, command + ['slow.py'], ttl='3s'), 124)
    assert expired['outcome'] == 'expired'
    assert (directory / 'uv.ready').exists(), 'uv workload never reached Python before expiry'
    try:
        os.killpg(expired['process_identity']['pgid'], 0)
    except ProcessLookupError:
        pass
    else:
        raise AssertionError('uv process group survived expiry')
    assert not (directory / 'uv.late').exists()
    f.cleanup(store, exported, verify)
    print('PASS uv/Python: offline wheel resolution/install and isolated venv/cache, exit=7, descendant cancellation, TTL expiry, exported zipapp and cleanup', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', default='bin/arm')
    parser.add_argument('--tools', nargs='+', choices=('cargo', 'gradle', 'uv'), default=['cargo', 'gradle', 'uv'])
    parser.add_argument('--allow-missing', action='store_true', help='explicitly skip missing prerequisites (default: fail)')
    opts = parser.parse_args()
    tools = {name: shutil.which(name) for name in ('cargo', 'rustc', 'gradle', 'java', 'uv')}
    tools['python'] = sys.executable
    selected = []
    for name in dict.fromkeys(opts.tools):
        missing = [tool for tool in {'cargo': ('cargo', 'rustc'), 'gradle': ('gradle', 'java'), 'uv': ('uv',)}[name] if not tools[tool]]
        if missing:
            if not opts.allow_missing:
                parser.error('missing prerequisites: ' + ', '.join(missing))
            print('SKIP ' + name + ': missing ' + ', '.join(missing), flush=True)
        else:
            selected.append(name)
    if not selected:
        parser.error('no real toolchains available')
    root = Path(tempfile.mkdtemp(prefix='arm real workloads ')).resolve()
    # Keep the installed Rust toolchain read-only, with writable Cargo caches and
    # every tool's user config rooted in this fixture instead of the real home.
    rustup = os.environ.get('RUSTUP_HOME', str(Path.home() / '.rustup'))
    env = {k: v for k, v in os.environ.items() if not k.startswith(('ARM_', 'CARGO_', 'RUST', 'UV_', 'GRADLE_', 'PYTHON')) and k not in ('JAVA_TOOL_OPTIONS', 'JAVA_OPTS', '_JAVA_OPTIONS', 'JDK_JAVA_OPTIONS', 'VIRTUAL_ENV')}
    (root / 'user').mkdir()
    (root / 'exports').mkdir()
    env.update(HOME=str(root / 'user'), RUSTUP_HOME=rustup, CARGO_HOME=str(root / 'cargo-home'),
               GRADLE_USER_HOME=str(root / 'gradle-version-home'), UV_CACHE_DIR=str(root / 'uv-version-cache'),
               UV_PYTHON_DOWNLOADS='never', UV_OFFLINE='1', CARGO_NET_OFFLINE='true',
               XDG_CONFIG_HOME=str(root / 'config'), XDG_CACHE_HOME=str(root / 'cache'))
    fixture = Fixture(str(Path(opts.arm).resolve()), root, env)
    try:
        for name in selected:
            version = checked([tools[name], '--version'], env)
            print(version, flush=True)
        if 'cargo' in selected:
            print(checked([tools['rustc'], '--version'], env), flush=True)
        print('Python ' + sys.version.split()[0], flush=True)
        fixture.cli('start', '--cpu', '2', '--disk', '2GiB')
        for name in selected:
            {'cargo': cargo_scenarios, 'gradle': gradle_scenarios, 'uv': uv_scenarios}[name](fixture, tools)
        print('All selected real-tool scenarios passed; Gradle/uv use generic supervision, not dedicated profiles.', flush=True)
    finally:
        fixture.close()


if __name__ == '__main__':
    main()
