#!/usr/bin/env python3
"""Compiled inbox/watch acceptance, using only test-owned state and processes."""
import argparse
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    opts = parser.parse_args()
    binary = str(Path(opts.arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-inbox-') as fixture:
        home = str(Path(fixture) / 'state')
        prefix = [binary, '--home', home]
        backend = None
        watch = None

        def cli(*args, check=True):
            result = subprocess.run(prefix + list(args), capture_output=True, text=True, timeout=10)
            if check:
                assert result.returncode == 0, (args, result.returncode, result.stderr)
                return json.loads(result.stdout) if result.stdout else None
            return result

        def start():
            process = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            children.append(process)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                result = cli('status', check=False)
                if result.returncode == 0:
                    return process
                assert process.poll() is None, process.stderr.read().decode()
                time.sleep(.05)
            raise TimeoutError('test backend did not start')

        def line(process, stream='stdout', seconds=10):
            pipe = getattr(process, stream)
            assert select.select([pipe], [], [], seconds)[0], 'watch did not produce ' + stream
            value = pipe.readline()
            assert value, 'watch exited unexpectedly: ' + str(process.poll())
            return value.decode()

        def watcher(*extra):
            process = subprocess.Popen(prefix + ['agent', 'watch', '--id', contact['id'], '--session', contact['session'], *extra], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            children.append(process)
            return process

        try:
            backend = start()
            contact = cli('agent', 'register', '--id', 'fixture', '--adapter', 'generic', '--delivery', 'poll')
            assert contact['session'] and contact['registration']
            inbox = ['--id', contact['id'], '--session', contact['session']]
            payload = 'Warning: reduce concurrency. $(do-not-execute)\nsecond line'
            first = cli('message', 'send', '--to', contact['id'], '--text', payload)
            assert first['deliveries']['fixture']['state'] == 'available'
            once = watcher('--once', '--wait', '0s')
            output, error = once.communicate(timeout=10)
            assert once.returncode == 0 and not error
            item = json.loads(output)
            assert item['message']['body'] == payload and not item['message']['deliveries']['fixture']['receipt']
            assert len(cli('agent', 'inbox', *inbox, '--pending')['items']) == 1, 'watch automatically acknowledged a message'
            received = cli('agent', 'receive', *inbox, '--message', first['id'])
            assert received.get('received_at') and not received.get('acknowledged_at')
            ack_args = ['agent', 'ack', *inbox, '--message', first['id'], '--text', 'Concurrency reduced']
            ack = cli(*ack_args)
            assert cli(*ack_args) == ack, 'retry created a second acknowledgement'
            assert sum(m.get('reply_to') == first['id'] for m in cli('messages')) == 1
            assert not cli('agent', 'inbox', *inbox, '--pending')['items']
            watch = watcher('--after', item['cursor'])
            second = cli('message', 'send', '--to', contact['id'], '--text', 'before restart')
            before = json.loads(line(watch))
            assert before['message']['id'] == second['id']
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            assert 'reconnecting' in line(watch, 'stderr')
            backend = start()
            third = cli('message', 'send', '--to', contact['id'], '--text', 'after restart')
            after = json.loads(line(watch))
            assert after['message']['id'] == third['id'], 'reconnect duplicated or skipped messages'
            assert before['registration'] == after['registration'] == contact['registration']
            watch.send_signal(signal.SIGINT)
            watch.communicate(timeout=10)
            assert watch.returncode == 130
            # Same contact name must not silently redirect an existing watcher.
            watch = watcher('--after', after['cursor'])
            replacement = cli('agent', 'register', '--id', 'fixture', '--adapter', 'generic', '--delivery', 'poll', '--session', str(uuid.uuid4()))
            stdout, stderr = watch.communicate(timeout=10)
            assert watch.returncode != 0 and not stdout and b'contact_changed' in stderr
            assert replacement['registration'] != contact['registration']
            old = cli('agent', 'inbox', '--id', replacement['id'], '--session', replacement['session'], '--after', after['cursor'], check=False)
            assert old.returncode != 0 and 'invalid_cursor' in old.stderr
            cli('daemon', 'stop')
            backend.wait(timeout=10)
            print('Compiled inbox: exact recipient, explicit receipt/ack, one operator reply, watch reconnect/cursor recovery, interrupt and contact replacement passed')
        finally:
            # Only terminate process handles created by this fixture. There are
            # no resource leases; wait before TemporaryDirectory removes state.
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
