#!/usr/bin/env python3
"""Exercise actual overage, polling acknowledgement, recovery and revocation."""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    opts = parser.parse_args()
    binary = str(Path(opts.arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-policy-') as fixture:
        root = Path(fixture)
        prefix = [binary, '--home', str(root / 'state')]

        def cli(*args):
            result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=15)
            assert result.returncode == 0, (args, result.returncode, result.stderr)
            return json.loads(result.stdout)

        def wait_for(check, seconds=15):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                value = check()
                if value:
                    return value
                time.sleep(.1)
            raise AssertionError('policy fixture did not reach expected state')

        try:
            backend = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            children.append(backend)
            wait_for(lambda: (root / 'state' / 'endpoint.json').exists())
            contact = cli('agent', 'register', '--id', 'policy-fixture', '--adapter', 'generic', '--delivery', 'poll', '--cwd', str(root))
            identity = ['--id', contact['id'], '--session', contact['session'], '--registration', contact['registration']]
            sentinel = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
            children.append(sentinel)
            cooperative = cli('acquire', '--cpu', '0.1', '--reason', 'unrelated cooperative permit')
            workload = '''import pathlib,sys,time
quiet=pathlib.Path(sys.argv[1])
deadline=time.monotonic()+45
n=1
while time.monotonic()<deadline:
 if quiet.exists(): time.sleep(.05)
 else:
  for _ in range(10000): n=(n*1664525+1013904223)&0xffffffff
'''
            for mode in ('warn', 'revoke'):
                quiet = root / ('quiet-' + mode)
                process = subprocess.Popen(prefix + ['run', '--agent', contact['id'], '--cpu', '0.1', '--usage-policy', mode, '--usage-window', '1s', '--usage-grace', '1s', '--usage-warnings', '3', '--usage-tolerance', '0', '--ttl', '40s', '--reason', 'policy ' + mode, '--json', '--', sys.executable, '-c', workload, str(quiet)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                children.append(process)
                lease = wait_for(lambda: next((l for l in cli('status')['leases'] if l['request']['reason'] == 'policy ' + mode), None))
                seen = set()

                def step():
                    incidents = cli('incidents', '--lease', lease['id'])
                    for incident in incidents:
                        assert incident['registration'] == contact['registration'] and incident['session'] == contact['session']
                        for message in incident['messages']:
                            if message not in seen:
                                cli('agent', 'ack', *identity, '--message', message, '--text', 'Observed warning; acknowledgement alone is not compliance')
                                seen.add(message)
                    return incidents[0] if incidents and incidents[0]['warning_count'] else None

                first = wait_for(step)
                assert first['resource'] == 'cpu' and first['evidence']['identity_verified']
                if mode == 'warn':
                    quiet.touch()
                    ended = wait_for(lambda: (i if (i := step()) and i['state'] == 'closed' else None))
                    assert ended['outcome'] == 'observed_within_threshold', ended
                    assert process.poll() is None and cli('inspect', lease['id'])['state'] == 'active'
                    cli('cancel', lease['id'])
                else:
                    ended = wait_for(lambda: (i if (i := step()) and i['state'] == 'closed' else None))
                    assert ended['outcome'] == 'revoked' and ended['warning_count'] == 3, ended
                    assert len(seen) == 3
                stdout, stderr = process.communicate(timeout=10)
                assert process.returncode == 125, (process.returncode, stderr)
                result = json.loads(stdout)
                assert result['outcome'] == ('cancelled' if mode == 'warn' else 'revoked')
                assert sentinel.poll() is None and cli('inspect', cooperative['id'])['state'] == 'active'
            cli('release', cooperative['id'])
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            # Reopen the actual database and inspect retained policy history.
            backend = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            children.append(backend)
            wait_for(lambda: (root / 'state' / 'endpoint.json').exists())
            history = cli('incidents')
            assert len(history) == 2 and {i['outcome'] for i in history} == {'revoked', 'observed_within_threshold'}
            assert all(i['state'] == 'closed' and i['policy']['warnings'] == 3 for i in history)
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            print('Compiled policy: native sustained overage, atomic polling warnings, acknowledgement, scoped recovery, three grace periods, owned revocation, unrelated survival and durable restart history passed')
        except Exception:
            try:
                history = cli('incidents')
                print(json.dumps({'incidents': history, 'usage': [cli('usage', i['lease']) for i in history]}, indent=2), file=sys.stderr)
            except Exception as diagnostic_error:
                print('Policy diagnostic unavailable:', diagnostic_error, file=sys.stderr)
            raise
        finally:
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()


if __name__ == '__main__':
    main()
