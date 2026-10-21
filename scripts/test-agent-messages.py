#!/usr/bin/env python3
"""Compiled agent send/reply acceptance in private state; no real agent sessions."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arm', default='bin/arm')
    binary = str(Path(parser.parse_args().arm).resolve())
    children = []
    with tempfile.TemporaryDirectory(prefix='arm-agent-messages-') as fixture:
        root = Path(fixture)
        fakebin = root / 'fakebin'
        fakebin.mkdir()
        log = root / 'native-calls'
        fake = fakebin / 'codex'
        fake.write_text('#!/bin/sh\nprintf "invoked\\n" >> ' + shlex.quote(str(log)) + '\nprintf "deliberate private native failure\\n" >&2\nexit 7\n')
        fake.chmod(0o700)
        env = dict(os.environ, PATH=str(fakebin) + os.pathsep + os.environ.get('PATH', ''))
        prefix = [binary, '--home', str(root / 'state')]

        def run(*args, text=None):
            return subprocess.run(prefix + list(args), input=text, capture_output=True, text=True, timeout=15, env=env)

        def cli(*args, text=None):
            result = run(*args, text=text)
            assert result.returncode == 0, (args, result.returncode, result.stdout, result.stderr)
            return json.loads(result.stdout)

        def failure(code, *args):
            result = run(*args)
            assert result.returncode != 0 and result.stdout == '', (args, result.returncode, result.stdout, result.stderr)
            assert json.loads(result.stderr)['error']['code'] == code, (args, result.stderr)
            return result

        def start():
            process = subprocess.Popen(prefix + ['serve'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, env=env)
            children.append(process)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if (root / 'state' / 'endpoint.json').exists():
                    return process
                assert process.poll() is None, process.stderr.read()
                time.sleep(.03)
            raise AssertionError('private messaging backend did not start')

        def identity(contact):
            return ['--id', contact['id'], '--session', contact['session'], '--registration', contact['registration']]

        def register(name, adapter='generic', delivery='poll', session=None):
            return cli('agent', 'register', '--id', name, '--adapter', adapter, '--delivery', delivery, '--session', session or str(uuid.uuid4()), '--cwd', str(root), '--config-dir', str(root / ('config-' + name)))

        def inbox(contact):
            return cli('agent', 'inbox', *identity(contact))['items']

        try:
            backend = start()
            assert "sender-aware-agent-messaging" in cli("status")["capabilities"]
            a, b = register('sender-a'), register('sender-b')
            send = ['agent', 'send', *identity(a), '--to', b['id'], '--to-registration', b['registration'], '--text', '-', '--key', 'exact-send']
            body = 'Please review this result. $(do-not-execute)\nUnicode: λ'
            message = cli(*send, text=body)
            assert message['sender'] == 'agent:' + a['id'] and message['sender_contact'] == a
            assert message['agent_operation_key'] == 'exact-send' and message['body'] == body
            assert message['deliveries'][b['id']]['state'] == 'available'
            assert [item['message']['id'] for item in inbox(b)] == [message['id']]
            assert inbox(a) == []
            assert cli(*send, text=body)['id'] == message['id']
            found = cli('agent', 'message-lookup', *identity(a), '--key', 'exact-send')
            assert found['found'] and found['message']['id'] == message['id']
            assert not cli('agent', 'message-lookup', *identity(b), '--key', 'exact-send')['found']
            failure('forbidden', 'agent', 'reply', *identity(a), '--message', message['id'], '--text', 'Not an original recipient', '--key', 'wrong-recipient')
            reply = cli('agent', 'reply', *identity(b), '--message', message['id'], '--text', 'Reviewed result', '--key', 'explicit-reply')
            assert reply['reply_to'] == message['id'] and reply['audience'] == a['id'] and reply['sender_contact'] == b
            assert cli('agent', 'reply', *identity(b), '--message', message['id'], '--text', 'Reviewed result', '--key', 'explicit-reply')['id'] == reply['id']
            receipt = cli('agent', 'ack', *identity(b), '--message', message['id'], '--text', 'Acknowledgement for original sender')
            source_messages = {item['message']['id']: item['message'] for item in inbox(a)}
            assert len(source_messages) == 2 and receipt['reply_id'] in source_messages
            ack_reply = source_messages[receipt['reply_id']]
            assert ack_reply['audience'] == a['id'] and ack_reply['reply_to'] == message['id']
            assert ack_reply['deliveries'][a['id']]['mode'] == 'poll' and ack_reply['sender_contact'] == b
            assert not any(m['audience'] == 'operator' for m in cli('messages')), 'agent reply/ack leaked into operator inbox'
            assert cli('agent', 'ack', *identity(b), '--message', message['id'], '--text', 'Acknowledgement for original sender')['reply_id'] == receipt['reply_id']
            failure('idempotency_conflict', 'agent', 'send', *identity(a), '--to', b['id'], '--text', 'Changed text', '--key', 'exact-send')
            failure('contact_changed', 'agent', 'send', *identity(a), '--to', b['id'], '--to-registration', 'not-the-registration', '--text', 'Do not retarget', '--key', 'pinned-recipient')

            old_source = cli('agent', 'send', *identity(a), '--to', b['id'], '--text', 'Reply only to this original source', '--key', 'before-source-replaced')
            replacement = register(a['id'])
            failure('contact_changed', 'agent', 'reply', *identity(b), '--message', old_source['id'], '--text', 'Never select the replacement', '--key', 'stale-source-reply')
            superseded = cli('agent', 'ack', *identity(b), '--message', old_source['id'], '--text', 'Preserve without retargeting')
            assert superseded['reply_state'] == 'superseded' and superseded['reply_error']
            assert inbox(replacement) == []
            failure('contact_changed', 'agent', 'message-lookup', *identity(a), '--key', 'exact-send')

            native = register('private-native-failure', 'codex', 'native')
            failed_args = ['agent', 'send', *identity(replacement), '--to', native['id'], '--text', 'Private fake native adapter only', '--key', 'failed-native']
            failed = run(*failed_args)
            assert failed.returncode == 1 and failed.stderr == '', (failed.returncode, failed.stdout, failed.stderr)
            persisted = json.loads(failed.stdout)
            assert persisted['deliveries'][native['id']]['state'] == 'failed'
            assert 'deliberate private native failure' in persisted['deliveries'][native['id']]['error']
            assert persisted['agent_operation_key'] == 'failed-native' and log.read_text().splitlines() == ['invoked']
            retried = run(*failed_args)
            assert retried.returncode == 1 and retried.stderr == '' and json.loads(retried.stdout)['id'] == persisted['id']
            assert log.read_text().splitlines() == ['invoked'], 'exact native retry invoked the adapter again'
            lookup = cli('agent', 'message-lookup', *identity(replacement), '--key', 'failed-native')
            assert lookup['message']['id'] == persisted['id'] and log.read_text().splitlines() == ['invoked']
            cli('stop')
            assert backend.wait(timeout=10) == 0
            backend = start()
            assert cli('agent', 'message-lookup', *identity(replacement), '--key', 'failed-native')['message']['id'] == persisted['id']
            assert log.read_text().splitlines() == ['invoked']
            cli('stop')
            assert backend.wait(timeout=10) == 0
            print('Compiled agent messaging: exact sender/recipient attribution, stdin text, reply proof, keyed retry/lookup, ack-to-sender polling, replacement refusal, isolated native failure single-result exit, no native replay and restart persistence passed')
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
