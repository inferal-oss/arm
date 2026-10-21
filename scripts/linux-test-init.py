#!/usr/bin/env python3
"""Reap adopted test descendants inside an explicitly created PID namespace.

SSH acceptance uses unshare --pid --fork --kill-child --mount-proc. Its PID 1
must reap orphaned compiler/JVM children, as a normal host init does. Otherwise
zombies keep process groups present and correctly prevent ARM reconciliation.
This helper changes only the test environment, never ARM's ownership checks.
"""
import os
import signal
import sys


def main():
    if sys.platform != 'linux' or os.getpid() != 1 or len(sys.argv) < 2:
        print('linux-test-init requires PID 1 in a private Linux PID namespace and a command', file=sys.stderr)
        return 2
    child = os.fork()
    if child == 0:
        try:
            os.execvp(sys.argv[1], sys.argv[1:])
        except OSError as error:
            print('test command could not start: ' + str(error), file=sys.stderr)
            os._exit(127)
    pidfd = os.pidfd_open(child) if hasattr(os, 'pidfd_open') and hasattr(signal, 'pidfd_send_signal') else None

    def forward(signum, frame):
        if child is None:
            return
        try:
            if pidfd is not None:
                signal.pidfd_send_signal(pidfd, signum)
            else:
                # The direct child has not been reaped, so its PID cannot yet
                # be reused. No persisted or unrelated process is selected.
                os.kill(child, signum)
        except ProcessLookupError:
            pass

    for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(signum, forward)
    try:
        while True:
            try:
                pid, status = os.waitpid(-1, 0)
            except InterruptedError:
                continue
            if pid == child:
                child = None
                code = os.waitstatus_to_exitcode(status)
                return code if code >= 0 else 128 - code
    finally:
        if pidfd is not None:
            os.close(pidfd)


if __name__ == '__main__':
    sys.exit(main())
