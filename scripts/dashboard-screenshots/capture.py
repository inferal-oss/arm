#!/usr/bin/env python3
"""Capture real ARM web and PTY dashboards using disposable build-owned state.

Intended for the Pages job on a fresh GitHub runner. No user installation,
existing ARM state, external peer or remote mesh authority is used. Browser
credentials and terminal bytes stay in a mode-0700 temporary directory that is
removed on both success and failure. Only PNGs and public provenance are output.
"""
import argparse
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

REPO = Path(__file__).resolve().parents[2]
HELPER = Path(__file__).resolve().parent
sys.path.insert(0, str(REPO / 'scripts'))
from owned_process import OwnershipError, deferred_signals, run_owned_command

PROCESS_STOP_TIMEOUT = 5
BACKEND_SAMPLE_SECONDS = 6
TERMINAL_FRAME_SECONDS = 6


class CaptureError(Exception):
    pass


def run(arguments, *, env, timeout=20, check=True):
    try:
        result = run_owned_command(arguments, cwd=REPO, env=env, timeout=timeout,
                                   stop_timeout=PROCESS_STOP_TIMEOUT)
    except OwnershipError as error:
        raise CaptureError(str(error)) from None
    if check and result.returncode:
        # Never print command arguments, raw output, browser URLs or credentials.
        code = 'command_failed'
        try:
            value = json.loads(result.stderr)
            candidate = value.get('error', {}).get('code', '')
            if re.fullmatch(r'[a-z_]{1,64}', candidate):
                code = candidate
        except (ValueError, AttributeError):
            pass
        renderer_stage = re.fullmatch(rb'Dashboard renderer failed at safe stage: ([A-Za-z ]{1,80})\n?', result.stderr)
        if renderer_stage:
            code = 'renderer_' + renderer_stage.group(1).decode().lower().replace(' ', '_')
        raise CaptureError(code)
    return result


def terminal_geometry(slave):
    grid = json.loads((HELPER / 'geometry.json').read_text())
    cols, rows = grid.get('cols'), grid.get('rows')
    if type(cols) is not int or type(rows) is not int or not 80 <= cols <= 200 or not 24 <= rows <= 120:
        raise CaptureError('invalid_terminal_geometry')
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    return grid


def terminate(process):
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()  # Only the exact owned subprocess, never a name or PID search.
        process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=REPO / 'website/public/screenshots')
    options = parser.parse_args()
    # The public images must never come from a user's live workstation or backend.
    if os.environ.get('GITHUB_ACTIONS') != 'true':
        raise CaptureError('requires_fresh_github_actions_runner')
    output = options.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    views = json.loads((HELPER / 'views.json').read_text())['images']
    names = tuple(view['file'] for view in views) + ('metadata.json',)
    for name in names:
        if (output / name).is_symlink():
            raise CaptureError('output_symlink_refused')
    stage = 'initialize'
    backend = terminal = log = None
    master = slave = None
    lease_id = None
    env = dict(os.environ, GOMAXPROCS='1', TERM='xterm-256color', COLORTERM='truecolor')
    for name in ('NO_COLOR', 'DEBUG', 'PWDEBUG', 'NODE_OPTIONS', 'ARM_HOME'):
        env.pop(name, None)
    with tempfile.TemporaryDirectory(prefix='arm-pages-capture-') as temp:
        private = Path(temp)
        os.chmod(private, 0o700)
        # Keep transient cache/state paths inside this original disposable directory.
        (private / 'tmp').mkdir()
        env['TMPDIR'] = str(private / 'tmp')
        home = private / 'state'
        binary = private / 'arm'
        prefix = [str(binary), '--home', str(home)]

        def cli(*arguments, check=True):
            result = run(prefix + list(arguments), env=env, check=check)
            return json.loads(result.stdout) if result.returncode == 0 and result.stdout else None

        try:
            # Always compile the embedded browser source before Go embeds it.
            # A caller-supplied executable cannot prove that binding.
            stage = 'snapshot dashboard source'
            inputs = json.loads(run(['node', str(HELPER / 'source.mjs'), 'snapshot'], env=env).stdout)
            stage = 'rebuild embedded dashboard'
            run(['npm', '--prefix', 'web', 'run', 'build'], env=env, timeout=180)
            stage = 'verify rebuilt dashboard'
            embedded_ui = json.loads(run(['node', str(HELPER / 'source.mjs'), 'verify'], env=env).stdout)
            if any(embedded_ui[key] != value for key, value in inputs.items()):
                raise CaptureError('dashboard_inputs_changed_during_build')
            stage = 'build CLI'
            run(['go', 'build', '-p', '1', '-o', str(binary), './cmd/arm'], env=env, timeout=600)
            stage = 'start private backend'
            # Disable this fixture's peer publication before the foreground backend starts.
            cli('peers', 'off')
            log = open(private / 'backend.log', 'wb')
            os.chmod(private / 'backend.log', 0o600)
            with deferred_signals() as previous:
                backend = subprocess.Popen(prefix + ['serve', '--cpu', '1', '--memory', 'auto', '--disk', 'auto'],
                                           cwd=REPO, env=env, stdout=log, stderr=log, start_new_session=True,
                                           preexec_fn=lambda: signal.pthread_sigmask(signal.SIG_SETMASK, previous))
            ready = time.monotonic() + 20
            while True:
                if backend.poll() is not None:
                    raise CaptureError('backend_exited')
                status = run(prefix + ['daemon', 'status'], env=env, timeout=3, check=False)
                if status.returncode == 0:
                    break
                if time.monotonic() >= ready:
                    raise CaptureError('backend_readiness_timeout')
                time.sleep(.15)
            stage = 'create example cooperative lease'
            pool = cli('pool', 'create', 'documentation-builds', '--scope', 'global', '--capacity', '2',
                       '--reason', 'Pages screenshot fixture')['after']['id']
            lease_id = cli('acquire', '--pool', pool + '=1', '--ttl', '5m',
                           '--reason', 'Example documentation build')['id']
            # Let the real collector obtain multiple native samples before the
            # terminal's first status read. Missing measurements remain missing.
            stage = 'collect initial host observations'
            time.sleep(BACKEND_SAMPLE_SECONDS)
            stage = 'capture terminal dashboard'
            master, slave = pty.openpty()
            grid = terminal_geometry(slave)
            original_mode = termios.tcgetattr(slave)
            with deferred_signals() as previous:
                terminal = subprocess.Popen(prefix + ['dashboard', 'terminal'], cwd=REPO,
                                            stdin=slave, stdout=slave, stderr=slave, env=env, start_new_session=True,
                                            preexec_fn=lambda: signal.pthread_sigmask(signal.SIG_SETMASK, previous))
            transcript = bytearray()

            def read_until(text, seconds=15, minimum_seconds=0, boundary=0):
                started = time.monotonic()
                deadline = started + seconds
                while time.monotonic() < deadline:
                    if select.select([master], [], [], .1)[0]:
                        try:
                            chunk = os.read(master, 65536)
                        except OSError as error:
                            if error.errno == errno.EIO:
                                raise CaptureError('terminal_closed') from None
                            raise
                        if not chunk:
                            raise CaptureError('terminal_closed')
                        transcript.extend(chunk)
                        if len(transcript) > 4 * 1024 * 1024:
                            raise CaptureError('terminal_capture_limit')
                    plain = re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]', b'', transcript[boundary:])
                    if terminal.poll() is not None:
                        raise CaptureError('terminal_exited_early')
                    if text.encode() in plain and time.monotonic() - started >= minimum_seconds:
                        return
                raise CaptureError('terminal_readiness_timeout')

            read_until('connected')
            read_until('RAM remaining')
            transcripts = {}
            for view in (view for view in views if view['surface'] == 'terminal'):
                # Require new bytes after navigation, rather than a label left
                # in an earlier transcript. The renderer separately checks the
                # final active screen, not just this byte-stream history.
                boundary = 0 if view['key'] == '1' else len(transcript)
                if view['key'] != '1':
                    os.write(master, view['key'].encode())
                read_until(view['labels'][0], minimum_seconds=TERMINAL_FRAME_SECONDS, boundary=boundary)
                transcript_path = private / (view['file'] + '.ansi')
                transcript_path.write_bytes(transcript)
                os.chmod(transcript_path, 0o600)
                transcripts[view['file']] = str(transcript_path)
            os.write(master, b'q')
            terminal.wait(timeout=10)
            if terminal.returncode:
                raise CaptureError('terminal_quit_failed')
            restored_mode = termios.tcgetattr(slave)
            restored_mode[3] &= ~getattr(termios, 'PENDIN', 0)
            original_mode[3] &= ~getattr(termios, 'PENDIN', 0)
            if restored_mode != original_mode:
                raise CaptureError('terminal_mode_not_restored')
            terminal = None
            stage = 'render real dashboard screenshots'
            # Issue the short-lived browser ticket only after the PTY capture.
            link = cli('dashboard', '--json')['url']
            endpoint = json.loads((home / 'endpoint.json').read_text())
            config = private / 'capture.json'
            config.write_text(json.dumps({'url': link, 'endpointToken': endpoint['token'],
                                          'transcripts': transcripts, 'views': views, 'output': str(output),
                                          'terminalGeometry': grid}))
            os.chmod(config, 0o600)
            # Node prints safe stage identifiers only; it never emits URLs,
            # screenshots' private bootstrap data, page console or traces.
            run(['node', str(HELPER / 'render.mjs'), str(config)], env=env, timeout=120)
            stage = 'verify capture source unchanged'
            if json.loads(run(['node', str(HELPER / 'source.mjs'), 'verify'], env=env).stdout) != embedded_ui:
                raise CaptureError('dashboard_inputs_changed_during_capture')
            stage = 'write public provenance'
            source = run(['git', 'rev-parse', 'HEAD'], env=env).stdout.decode().strip()
            if not re.fullmatch(r'[0-9a-f]{40}', source):
                raise CaptureError('invalid_source_revision')
            entries = []
            for name in names[:-1]:
                data = (output / name).read_bytes()
                if not data.startswith(b'\x89PNG\r\n\x1a\n'):
                    raise CaptureError('invalid_capture_png')
                width, height = struct.unpack('>II', data[16:24])
                entries.append({'file': name, 'sha256': hashlib.sha256(data).hexdigest(),
                                'width': width, 'height': height})
            metadata = {'schema': 2, 'source_commit': source, 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                        'embedded_ui': embedded_ui,
                        'scope': 'Disposable GitHub Actions backend, real host observations and example cooperative pool lease.',
                        'terminal': 'Actual arm dashboard terminal PTY bytes rendered by xterm.js.',
                        'sampling': {'backend_warmup_seconds': BACKEND_SAMPLE_SECONDS,
                                     'terminal_frame_seconds': TERMINAL_FRAME_SECONDS},
                        'terminal_geometry': grid,
                        'terminal_profile': {'background': '#101519', 'foreground': '#e1e9ed',
                                             'font_family': 'DejaVu Sans Mono, monospace', 'font_size': 14},
                        'images': entries}
            (output / 'metadata.json').write_text(json.dumps(metadata, indent=2) + '\n')
            print('Captured five web views and three terminal views from rebuilt current-source dashboards.')
        except Exception as error:
            # Remove partial publication outputs; fail the Pages build instead of
            # silently shipping stale or mixed-source pictures.
            for name in names:
                (output / name).unlink(missing_ok=True)
            safe = str(error) if isinstance(error, CaptureError) else type(error).__name__
            raise CaptureError(stage + ': ' + safe) from None
        finally:
            terminate(terminal)
            for fd in (master, slave):
                if fd is not None:
                    os.close(fd)
            if backend is not None and backend.poll() is None:
                if lease_id:
                    try:
                        cli('release', lease_id)
                    except Exception:
                        pass
                try:
                    cli('daemon', 'stop')
                    backend.wait(timeout=10)
                except Exception:
                    terminate(backend)
            if backend is not None:
                backend.wait(timeout=5)
            if log is not None:
                log.close()


if __name__ == '__main__':
    # TERM/INT still execute the same exact-owner cleanup path.
    def interrupted(_signum, _frame):
        raise CaptureError('capture_interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    signal.alarm(900)
    try:
        main()
    except Exception as error:
        safe = str(error) if isinstance(error, CaptureError) else type(error).__name__
        print('Dashboard capture failed: ' + safe, file=sys.stderr)
        sys.exit(1)
