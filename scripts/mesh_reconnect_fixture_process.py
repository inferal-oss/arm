"""Bounded owned subprocess transport for disposable reconnect acceptance only.

The leader remains unreaped until final group signals are complete. A normal
successful wait ends signalling authority; a remaining group is unknown and is
never signalled through a saved reaped PID. Groups are cooperative cleanup scopes,
not containment of detached ARM backends. Native Popen/I/O is a trusted operation.
"""
import os
import selectors
import signal
import subprocess
import time


class CommandFailure(RuntimeError):
    def __init__(self, message, receipt, stdout, stderr):
        super().__init__(message)
        self.receipt, self.stdout, self.stderr = receipt, stdout, stderr


def bounded_command(argv, stdin=None, env=None, timeout=120, output_limit=1 << 20):
    if not 0 < timeout <= 120 or not 0 < output_limit <= 1 << 20:
        raise ValueError('positive fixture command timeout <=120s and output <=1MiB required')
    payload = stdin.encode() if isinstance(stdin, str) else stdin or b''
    if not isinstance(payload, bytes) or len(payload) > 1 << 20:
        raise ValueError('fixture stdin must be <=1MiB bytes/UTF8 text')
    argv = [str(value) for value in argv]
    started = time.monotonic()
    selector = selectors.DefaultSelector()
    try:
        process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, env=env, start_new_session=True)
    except BaseException:
        selector.close()
        raise
    receipt = {'pid': process.pid, 'process_group': process.pid, 'argv': argv,
               'timeout_seconds': timeout, 'output_limit_per_stream': output_limit,
               'signals': [], 'signal_errors': [], 'signal_refusals_after_wait': [],
               'wait_joined': False, 'output_joined': False, 'group_absence_observed': False}
    buffers = {'stdout': bytearray(), 'stderr': bytearray()}
    counts = {'stdout': 0, 'stderr': 0}
    eof = set()
    offset = 0
    failure = None
    cancel_at = None
    killed = False
    signal_authority = True

    def send(sig):
        # Only this thread owns successful wait/reap. No poll, SIGCHLD reaper or
        # callback may consume the retained leader while signalling is possible.
        # Starting any wait retires this authority before native reap can occur.
        if not signal_authority or process.returncode is not None:
            receipt['signal_refusals_after_wait'].append(signal.Signals(sig).name)
            return
        receipt['signals'].append(signal.Signals(sig).name)
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
        except OSError as error:
            receipt['signal_errors'].append({'signal': int(sig), 'errno': error.errno})

    def wait_owned(timeout):
        nonlocal signal_authority
        # Retire BEFORE entering waitpid. Python may reap natively and then be
        # interrupted before it assigns Popen.returncode; that gap is unknown.
        signal_authority = False
        try:
            return process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            # Under this sole waiter, this explicit result means no native reap
            # occurred. A now-exited child remains an unreaped PID anchor.
            signal_authority = True
            raise

    def cancel(message):
        nonlocal failure, cancel_at
        if failure is None:
            failure = message
            cancel_at = time.monotonic()
            send(signal.SIGTERM)

    def read_ready(wait):
        nonlocal offset
        for key, _ in selector.select(wait):
            stream, name = key.fileobj, key.data
            try:
                if name == 'stdin':
                    offset += os.write(stream.fileno(), payload[offset:offset + 65536])
                    if offset == len(payload):
                        selector.unregister(stream)
                        stream.close()
                else:
                    raw = os.read(stream.fileno(), 65536)
                    if not raw:
                        eof.add(name)
                        selector.unregister(stream)
                        stream.close()
                    else:
                        counts[name] += len(raw)
                        remaining = max(0, output_limit - len(buffers[name]))
                        buffers[name].extend(raw[:remaining])
                        if counts[name] > output_limit:
                            cancel('fixture command output exceeded the per-stream bound')
            except (BrokenPipeError, ConnectionResetError):
                selector.unregister(stream)
                stream.close()
            except BlockingIOError:
                pass

    try:
        for stream, name, event in ((process.stdout, 'stdout', selectors.EVENT_READ),
                                    (process.stderr, 'stderr', selectors.EVENT_READ),
                                    (process.stdin, 'stdin', selectors.EVENT_WRITE)):
            os.set_blocking(stream.fileno(), False)
            if name == 'stdin' and not payload:
                stream.close()
            else:
                selector.register(stream, event, name)
        # Pipe readiness is observed without poll/wait. Even an exited leader
        # remains a native unreaped PID/group anchor through final cancellation.
        while selector.get_map():
            now = time.monotonic()
            if now - started >= timeout:
                cancel('fixture command timed out; mutation outcome must be inspected, never replayed')
            if cancel_at is not None and now - cancel_at >= .5 and not killed:
                send(signal.SIGKILL)
                killed = True
            if cancel_at is not None and now - cancel_at >= 6:
                break
            read_ready(.02)
        if failure:
            if not killed:
                send(signal.SIGKILL)
                killed = True
            wait_owned(timeout=1)
        else:
            try:
                # No signals follow a successful normal wait. Timeout does not
                # reap; the retained child still anchors the cancellation below.
                wait_owned(timeout=max(.001, timeout - (time.monotonic() - started)))
            except subprocess.TimeoutExpired:
                cancel('fixture command timed out after pipe EOF; mutation outcome unknown')
                time.sleep(.5)
                send(signal.SIGKILL)
                killed = True
                wait_owned(timeout=1)
        receipt['wait_joined'] = True
    except BaseException as error:
        if failure is None:
            failure = 'fixture command interrupted: ' + type(error).__name__
            receipt['interrupted'] = type(error).__name__
        # Any non-timeout wait interruption/error may have consumed native reap,
        # even with returncode still None. Never revive saved group authority.
        if signal_authority and process.returncode is None:
            send(signal.SIGTERM)
            if not killed:
                send(signal.SIGKILL)
                killed = True
            try:
                drain_until = time.monotonic()+1
                while selector.get_map() and time.monotonic()<drain_until:
                    read_ready(.02)
                wait_owned(timeout=1)
                receipt['wait_joined'] = True
            except BaseException as join_error:
                receipt['wait_error'] = repr(join_error)
        elif process.returncode is not None:
            receipt['wait_joined'] = True
        else:
            receipt['wait_ownership_ambiguous'] = True
    finally:
        for key in list(selector.get_map().values()):
            selector.unregister(key.fileobj)
            key.fileobj.close()
        selector.close()
        for stream in (process.stdin, process.stdout, process.stderr):
            if not stream.closed:
                stream.close()
        receipt['exit'] = process.returncode
        # Read-only observation after the final wait attempt, including an
        # ambiguous one. Presence never revives group signalling authority.
        try:
            os.killpg(process.pid, 0)
        except ProcessLookupError:
            receipt['group_absence_observed'] = True
        except OSError as error:
            receipt['group_observation_errno'] = error.errno
        receipt['output_joined'] = eof == {'stdout', 'stderr'}
        if failure is None and not (receipt['wait_joined'] and receipt['output_joined'] and receipt['group_absence_observed']):
            failure = 'owned fixture closure unconfirmed; no signalling after reap'
        receipt['bytes_observed'] = counts
        receipt['bytes_retained'] = {name: len(value) for name, value in buffers.items()}
        receipt['elapsed_seconds'] = time.monotonic() - started
        receipt['wait_ownership_ambiguous'] = not signal_authority and process.returncode is None
        receipt['signal_authority_retained'] = signal_authority
        receipt['error'] = failure
    stdout, stderr = (bytes(buffers[name]).decode('utf-8', errors='replace') for name in ('stdout', 'stderr'))
    if failure:
        raise CommandFailure(failure, receipt, stdout, stderr)
    return subprocess.CompletedProcess(argv, receipt['exit'], stdout, stderr), receipt
