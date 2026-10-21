#!/usr/bin/env python3
"""Real offline Gradle persistent-service acceptance, using isolated ARM homes.

Requires installed Gradle and a JDK, never downloads or adopts shared caches.
Run Linux under scripts/linux-test-init.py in a private PID namespace. Failed
fixtures are retained. Fixture-only Gradle --stop is explicit operator recovery
of daemons created exclusively by this script, never ARM's implementation.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time


def wait_for(fn, timeout=45):
    end = time.monotonic() + timeout
    last = None
    while time.monotonic() < end:
        last = fn()
        if last:
            return last
        time.sleep(.1)
    raise AssertionError(('condition timed out', last))


class Fixture:
    def __init__(self, arm, gradle, java_home):
        self.root = Path(tempfile.mkdtemp(prefix='arm-gradle-services-')).resolve()
        self.arm, self.gradle = arm, gradle
        self.home = self.root / 'authority'
        self.env = {k: v for k, v in os.environ.items() if k not in (
            'JAVA_HOME', 'JAVA_OPTS', 'GRADLE_OPTS', 'JAVA_TOOL_OPTIONS',
            'JDK_JAVA_OPTIONS', '_JAVA_OPTIONS', 'GRADLE_USER_HOME')}
        self.env.update(JAVA_HOME=java_home, HOME=str(self.root / 'user'),
                        XDG_CONFIG_HOME=str(self.root / 'config'),
                        XDG_CACHE_HOME=str(self.root / 'cache'))
        Path(self.env['HOME']).mkdir()
        self.children, self.daemon = [], None
        self.stores, self.services = [], []
        self.log = (self.root / 'backend.log').open('a')
        self.foreign_home = self.root / 'unrelated-daemon-home'
        self.foreign_project = self.root / 'unrelated-project'
        self.foreign_project.mkdir()
        self.foreign_log = (self.root / 'unrelated-daemon.log').open('w+')
        self.foreign = subprocess.Popen([self.gradle, '--offline', '--foreground',
                                         '--gradle-user-home', str(self.foreign_home),
                                         '-Dorg.gradle.jvmargs=-Xms64m -Xmx128m -XX:MaxMetaspaceSize=128m'],
                                        cwd=self.foreign_project, env=self.env,
                                        stdout=self.foreign_log, stderr=self.foreign_log,
                                        start_new_session=True)
        def foreign_ready():
            assert self.foreign.poll() is None, 'unrelated fixture daemon exited'
            self.foreign_log.seek(0)
            return 'Daemon server started.' in self.foreign_log.read()
        wait_for(foreign_ready)
        self.start()

    def start(self):
        self.daemon = subprocess.Popen([self.arm, '--home', str(self.home), 'serve',
                                       '--cpu', '6', '--memory', '4GiB', '--disk', '8GiB'],
                                      env=self.env, stdout=self.log, stderr=self.log)
        wait_for(lambda: (self.home / 'endpoint.json').exists() and self.cli('status'), 20)

    def raw(self, *args):
        return subprocess.run([self.arm, '--home', str(self.home), *args], env=self.env,
                              capture_output=True, text=True, timeout=90)

    def cli(self, *args):
        result = self.raw(*args)
        assert result.returncode == 0, (args, result.returncode, result.stdout, result.stderr)
        return json.loads(result.stdout)

    def rejected(self, code, *args):
        result = self.raw(*args)
        assert result.returncode != 0, ('accepted unsafe request', args, result.stdout)
        value = json.loads(result.stderr)
        assert value['error']['code'] == code, (args, value, result.stdout)

    def store(self, name):
        store = self.cli('storage', 'create', '1GiB', '--ttl', '15m', '--reason', name)
        self.stores.append(store)
        return store

    def service(self, reason='persistent service', ttl='8m', memory='1GiB'):
        store = self.store(reason + ' private state')
        service = self.cli('run', '--profile', 'gradle-service', '--detach', '--storage', store['id'],
                           '--cpu', '2', '--memory', memory, '--ttl', ttl, '--reason', reason,
                           '--', self.gradle)
        assert service['workload']['state'] in ('starting', 'probing'), service
        self.services.append(service)

        def ready():
            current = self.cli('inspect', service['id'])
            assert current['state'] == 'active', current
            assert current['workload']['state'] not in ('blocked', 'unresolved'), current
            return current if current['workload']['state'] == 'ready' else None
        service = wait_for(ready)
        assert service['workload']['service_process'] == service['process_identity'], service
        assert service['workload']['worker_limit'] == 2, service
        return service, store

    def project(self, name, extra=''):
        project = self.root / name
        (project / 'src/main/java').mkdir(parents=True)
        (project / 'settings.gradle').write_text("rootProject.name = 'arm-output'\n")
        (project / 'src/main/java/Main.java').write_text(
            'public class Main { public static void main(String[] args) { System.out.println("arm-gradle-retained"); } }\n')
        (project / 'build.gradle').write_text("""plugins { id 'java' }
file('configured').text='configured'
jar { manifest { attributes('Main-Class': 'Main') } }
tasks.register('evidence') { doLast {
    file('evidence.json').text = groovy.json.JsonOutput.toJson([
        pid:ProcessHandle.current().pid(),
        start:ProcessHandle.current().info().startInstant().get().toEpochMilli(),
        workers:gradle.startParameter.maxWorkerCount,
        build:layout.buildDirectory.get().asFile.canonicalPath,
        home:gradle.gradleUserHomeDir.canonicalPath,
        java:System.getProperty('java.home')])
} }
tasks.register('slow') { doLast { file('busy').text='busy'; Thread.sleep(6000); file('finished').text='finished' } }
tasks.register('broken') { doLast { throw new GradleException('intentional real build failure') } }
""" + extra)
        return project

    def args(self, service, store, project, reason, tasks, ttl='1m'):
        return ['run', '--profile', 'gradle-build', '--service', service['id'],
                '--storage', store['id'], '--cwd', str(project), '--cpu', '.25',
                '--memory', '256MiB', '--ttl', ttl, '--reason', reason, '--json',
                '--', self.gradle, '--offline', *tasks]

    def launch(self, service, store, project, reason, tasks, ttl='1m'):
        out = (self.root / (reason + '.json')).open('w+')
        err = (self.root / (reason + '.log')).open('w+')
        process = subprocess.Popen([self.arm, '--home', str(self.home), *self.args(service, store, project, reason, tasks, ttl)],
                                   env=self.env, stdout=out, stderr=err, text=True)
        handle = (process, out, err, reason)
        self.children.append(handle)
        return handle

    def finish(self, handle, expected=0, terminal=True):
        process, out, err, reason = handle
        process.wait(timeout=90)
        out.seek(0)
        err.seek(0)
        stdout, stderr = out.read(), err.read()
        allowed = expected if isinstance(expected, tuple) else (expected,)
        assert process.returncode in allowed, (reason, process.returncode, stderr[-16000:], stdout)
        lease = json.loads(stdout)
        if terminal:
            assert lease['state'] == 'terminal' and lease['workload']['state'] == 'settled', lease
        return lease

    def active(self, reason):
        return next(l for l in self.cli('status')['leases'] if l['request']['reason'] == reason)

    def terminal(self, lease):
        return wait_for(lambda: (lambda l: l if l['state'] == 'terminal' else None)(self.cli('inspect', lease['id'])))

    def fixture_stop(self, service, project):
        # Only fixtures' freshly created private home is selected. This is an
        # external operator intervention, not evidence that ARM owns fallback PIDs.
        value = service['workload']
        assert str(Path(value['home']).resolve()).startswith(str(self.root) + os.sep)
        result = subprocess.run([self.gradle, '--offline', '--gradle-user-home', value['home'], '--stop'],
                                cwd=project, env=self.env, capture_output=True, text=True, timeout=30)
        assert result.returncode == 0, result.stderr

    def close(self):
        # Preserve all state if ARM cannot establish that execution stopped.
        for service in self.services:
            current = self.cli('inspect', service['id'])
            if current['state'] == 'active':
                self.cli('release', service['id'])
            current = self.cli('inspect', service['id'])
            if current['state'] == 'needs_attention':
                self.cli('reconcile', service['id'])
            self.terminal(service)
        for handle in self.children:
            handle[0].wait(timeout=20)
            handle[1].close()
            handle[2].close()
        assert not [l for l in self.cli('status')['leases'] if l['state'] != 'terminal' and l['request'].get('kind') != 'storage']
        for store in self.stores:
            current = self.cli('inspect', store['id'])
            if current['state'] != 'terminal':
                if Path(store['managed_dir']).exists():
                    assert self.cli('storage', 'cleanup', store['id'], '--dry-run')['can_clean']
                # Explicit fixture handoff is not an assertion of reclaimed bytes.
                self.cli('storage', 'handoff', store['id'])
        self.cli('stop')
        self.daemon.wait(timeout=20)
        assert self.foreign.poll() is None, 'ARM disrupted an unrelated Gradle daemon'
        os.killpg(self.foreign.pid, signal.SIGTERM)
        self.foreign.wait(timeout=20)
        self.foreign_log.close()
        self.log.close()
        assert not (self.home / 'endpoint.json').exists()
        shutil.rmtree(self.root)


def scenarios(f):
    minimum, minimum_store = f.service('minimum service', memory='512MiB')
    minimum_output = f.store('minimum outputs')
    minimal = f.project('minimum project')
    (minimal / 'build.gradle').write_text("tasks.register('proof') { doLast { file('pid').text=String.valueOf(ProcessHandle.current().pid()) } }\n")
    for index in range(2):
        f.finish(f.launch(minimum, minimum_output, minimal, 'minimum-' + str(index), ['proof']))
        assert int((minimal / 'pid').read_text()) == minimum['pid']
    f.cli('release', minimum['id'])
    f.terminal(minimum)
    for store in [minimum_store, minimum_output]:
        assert f.cli('storage', 'cleanup', store['id'], '--dry-run')['can_clean']
        f.cli('storage', 'handoff', store['id'])
    print('PASS minimum512MiB service ready/reuse with startup allowance inside its single promise', flush=True)

    service, service_store = f.service()
    output = f.store('separate client output')
    project = f.project('java project')
    assert 'owned-gradle-services' in f.cli('status')['capabilities']
    f.rejected('profile_conflict', *f.args(service, service_store, project, 'shared-service-output-refusal', ['jar']))
    for index in range(2):
        lease = f.finish(f.launch(service, output, project, 'java-' + str(index), ['jar', 'evidence']))
        proof = json.loads((project / 'evidence.json').read_text())
        assert proof['pid'] == service['pid'] and proof['start'] == service['workload']['service_start_millis'], proof
        assert proof['workers'] == 2 and proof['home'] == service['workload']['home'], proof
        assert proof['build'].startswith(lease['workload']['build_directory'] + os.sep), proof
        assert not (project / 'build').exists(), 'project outputs escaped managed storage'
        assert f.cli('status')['reserved']['cpu'] == 2, 'persistent service promise dropped or client promise leaked'
        assert f.cli('status')['reserved']['memory_bytes'] == 1 << 30
    print('PASS real Java JAR builds, exact persistent JVM reuse and separate client/service promises', flush=True)

    failed = f.finish(f.launch(service, output, project, 'intentional-failure', ['broken']), expected=1)
    assert failed['outcome'] == 'failed', failed
    assert f.cli('inspect', service['id'])['workload']['state'] == 'ready'

    # Public profile options cannot override destination/JDK/daemon/worker policy.
    for option in ['--max-workers=8', '-Dorg.gradle.jvmargs=-Xmx128m', '--no-daemon', '-g', '--init-script=x']:
        f.rejected('profile_conflict', *f.args(service, output, project, 'override-refusal', [option, 'jar']))
    criteria = project / 'gradle/gradle-daemon-jvm.properties'
    criteria.parent.mkdir()
    criteria.write_text('toolchainVersion=17\n')
    f.rejected('profile_conflict', *f.args(service, output, project, 'jdk-refusal', ['jar']))
    criteria.unlink()
    (project / 'buildSrc').mkdir()
    f.rejected('profile_conflict', *f.args(service, output, project, 'buildsrc-refusal', ['jar']))
    (project / 'buildSrc').rmdir()
    override = f.project('worker override', "gradle.startParameter.maxWorkerCount = 8\ntasks.register('never') { doLast { file('task-ran').text='bad' } }\n")
    f.finish(f.launch(service, output, override, 'project-override', ['never']), expected=1)
    assert not (override / 'task-ran').exists()
    print('PASS failures and conflicting JDK/JVM/worker/daemon configuration refused without losing service', flush=True)

    slow = f.launch(service, output, project, 'busy-client', ['slow'])
    wait_for(lambda: (project / 'busy').exists())
    active = f.active('busy-client')
    reserved = f.cli('status')['reserved']
    assert reserved['cpu'] == 2.25 and reserved['memory_bytes'] == (1 << 30) + (256 << 20), reserved
    f.rejected('service_busy', *f.args(service, output, project, 'overlap-refusal', ['jar']))
    assert not f.cli('storage', 'cleanup', service_store['id'], '--dry-run')['can_clean']
    f.cli('cancel', active['id'])
    # Some Gradle/JDKs deliver a cancellation receipt before the follower sees
    # the unresolved intermediate state; either truthful final CLI code is valid.
    f.finish(slow, expected=(1, 125), terminal=False)
    settled = f.terminal(active)
    assert settled['outcome'] == 'cancelled', settled
    wait_for(lambda: f.cli('inspect', service['id'])['workload']['state'] == 'ready')
    assert f.cli('status')['reserved']['cpu'] == 2
    print('PASS serialized consumers, client cancellation with late verified completion and service retention', flush=True)

    (project / 'busy').unlink()
    loss = f.launch(service, output, project, 'receipt-loss', ['slow'])
    wait_for(lambda: (project / 'busy').exists())
    loss_lease = f.active('receipt-loss')
    receipt_dir = Path(loss_lease['workload']['build_directory']).parent / 'receipts'
    original = receipt_dir.with_name('original-receipts')
    receipt_dir.rename(original)
    receipt_dir.mkdir(mode=0o700)
    unresolved = f.finish(loss, expected=1, terminal=False)
    assert unresolved['state'] == 'needs_attention', unresolved
    assert f.cli('inspect', service['id'])['workload']['state'] == 'blocked'
    assert f.cli('status')['reserved']['cpu'] == 2.25
    assert (receipt_dir / 'finished.json').exists(), 'real JVM did not produce completion receipt'
    # Restoring the original directory and its actual matching JVM receipt is
    # explicit fixture intervention; a replacement inode was never adopted.
    (receipt_dir / 'finished.json').rename(original / 'finished.json')
    receipt_dir.rmdir()
    original.rename(receipt_dir)
    assert f.terminal(loss_lease)['outcome'] == 'completed'
    wait_for(lambda: f.cli('inspect', service['id'])['workload']['state'] == 'ready')
    print('PASS replacement receipt directory remains charged until original identity and actual late evidence return', flush=True)

    # Output is independently retained; source cleanup remains an explicit step.
    jar = next(Path(lease['workload']['build_directory']).rglob('*.jar'))
    destination = f.root / 'retained'
    operation = f.cli('storage', 'export', output['id'], str(destination), '--file',
                      str(jar.relative_to(output['managed_dir'])) + '=app.jar', '--reason', 'retain requested JAR', '--wait')
    assert operation['state'] == 'published', operation
    digest = hashlib.sha256((destination / 'app.jar').read_bytes()).hexdigest()
    f.cli('release', output['id'])
    wait_for(lambda: f.cli('inspect', output['id']).get('artifact', {}).get('namespace_removed'))
    result = subprocess.run([str(Path(f.env['JAVA_HOME']) / 'bin/java'), '-jar', str(destination / 'app.jar')],
                            env=f.env, capture_output=True, text=True, timeout=20)
    assert result.returncode == 0 and result.stdout.strip() == 'arm-gradle-retained', (result.stdout, result.stderr)
    assert hashlib.sha256((destination / 'app.jar').read_bytes()).hexdigest() == digest
    print('PASS exported executable JAR survives intermediate storage release and still runs', flush=True)

    # Deliberately occupy the fixture's exclusive service with an external
    # fixture client to exercise public Gradle's unexpected fallback behavior.
    fallback_output = f.store('fallback client output')
    other = f.project('fallback project')
    value = service['workload']
    external_log = (f.root / 'external.log').open('w')
    external = subprocess.Popen([f.gradle, '--offline', '--daemon', '--no-parallel', '--no-watch-fs',
                                 '--gradle-user-home', value['home'], '--max-workers=2',
                                 '-Dorg.gradle.jvmargs=' + ' '.join(value['jvm_args']),
                                 '-Dorg.gradle.java.home=' + value['java_home'], 'slow'],
                                cwd=other, env=f.env, stdout=external_log, stderr=external_log)
    wait_for(lambda: (other / 'busy').exists())
    guarded = f.project('guarded fallback')
    fallback = f.finish(f.launch(service, fallback_output, guarded, 'unexpected-fallback', ['jar']), expected=1, terminal=False)
    assert fallback['state'] == 'needs_attention', fallback
    assert not (guarded / 'configured').exists(), 'unexpected daemon ran project configuration'
    assert f.cli('inspect', service['id'])['workload']['state'] == 'blocked'
    assert f.cli('status')['reserved']['cpu'] == 2.25, 'unknown fallback was silently uncharged'
    f.rejected('service_unavailable', *f.args(service, fallback_output, project, 'blocked-reuse', ['jar']))
    external.wait(timeout=30)
    external_log.close()
    f.fixture_stop(service, project)
    wait_for(lambda: f.cli('inspect', service['id'])['state'] != 'active')
    f.cli('reconcile', service['id'])
    f.terminal(fallback)
    f.terminal(service)
    print('PASS unexpected fallback blocked before project settings/tasks, retained obligations, explicit external recovery', flush=True)

    # A killed backend cannot regain signal authority from durable saved PIDs.
    recovered, _ = f.service('restart service')
    pid = recovered['pid']
    f.daemon.kill()
    f.daemon.wait(timeout=20)
    (f.home / 'endpoint.json').unlink(missing_ok=True)
    f.start()
    uncertain = f.cli('inspect', recovered['id'])
    assert uncertain['state'] == 'needs_attention', uncertain
    os.kill(pid, 0)
    assert f.cli('status')['reserved']['cpu'] == 2
    f.rejected('needs_attention', 'reconcile', recovered['id'])
    os.kill(pid, 0)
    f.fixture_stop(recovered, project)
    wait_for(lambda: not process_exists(pid))
    f.cli('reconcile', recovered['id'])
    f.terminal(recovered)
    print('PASS backend restart preserves service promises and never signals a recovered PID', flush=True)

    composite_service, _ = f.service('unsupported composite service')
    composite_output = f.store('unsupported composite output')
    composite = f.project('composite project')
    included = f.project('included project')
    (composite / 'settings.gradle').write_text("rootProject.name='composite'\nincludeBuild('../included project')\n")
    refused = f.finish(f.launch(composite_service, composite_output, composite, 'composite-refusal', ['jar']), expected=1, terminal=False)
    assert refused['state'] == 'needs_attention', refused
    assert not (composite / 'configured').exists() and not (included / 'configured').exists()
    assert f.cli('inspect', composite_service['id'])['workload']['state'] == 'blocked'
    f.fixture_stop(composite_service, project)
    wait_for(lambda: f.cli('inspect', composite_service['id'])['state'] != 'active')
    f.cli('reconcile', composite_service['id'])
    f.terminal(refused)
    print('PASS unsupported composite cannot configure project tasks and is retained until explicit idle recovery', flush=True)

    expiring, _ = f.service('expiring service', ttl='20s')
    expiry_output = f.store('expiring work output')
    expiry_project = f.project('expiring work', "tasks.register('longWork') { doLast { file('busy').text='busy'; Thread.sleep(60000); file('escaped-completion').text='bad' } }\n")
    expiry_client = f.launch(expiring, expiry_output, expiry_project, 'expiring-client', ['longWork'], ttl='5s')
    wait_for(lambda: (expiry_project / 'busy').exists())
    work = f.active('expiring-client')
    wait_for(lambda: f.cli('inspect', expiring['id'])['state'] not in ('active', 'ending'), 35)
    f.finish(expiry_client, expected=(1, 124), terminal=False)
    assert not (expiry_project / 'escaped-completion').exists()
    # If termination prevented the final receipt, verified absence/idle remains
    # an explicit reconciliation step rather than expiry silently freeing it.
    current = f.cli('inspect', expiring['id'])
    if current['state'] == 'needs_attention':
        assert f.cli('status')['reserved']['cpu'] >= 2
        f.cli('reconcile', expiring['id'])
    f.terminal(expiring)
    f.terminal(work)
    print('PASS real client/service expiry stops owned scopes; missing proof stays charged until verified reconciliation', flush=True)


def process_exists(pid):
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arm', required=True)
    parser.add_argument('--gradle', default='gradle')
    options = parser.parse_args()
    arm = str(Path(options.arm).resolve())
    gradle = shutil.which(options.gradle)
    assert gradle, 'installed Gradle is required (no download or mock fallback)'
    java_home = os.environ.get('JAVA_HOME')
    if not java_home and os.uname().sysname == 'Darwin':
        java_home = subprocess.check_output(['/usr/libexec/java_home'], text=True).strip()
    if not java_home:
        java = shutil.which('java')
        assert java, 'installed JDK is required'
        java_home = str(Path(java).resolve().parent.parent)
    assert (Path(java_home) / 'bin/javac').exists(), 'a JDK with javac is required'
    fixture = Fixture(arm, gradle, java_home)
    try:
        scenarios(fixture)
        fixture.close()
    except BaseException:
        print('Retained isolated Gradle fixture:', fixture.root, flush=True)
        raise
    print('PASS real persistent Gradle service acceptance')


if __name__ == '__main__':
    main()
