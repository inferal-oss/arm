#!/usr/bin/env python3
"""Exercise the compiled daemon's port activation, revocation, and recovery."""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time


def serve(marker):
    ports = json.loads(os.environ["ARM_PORTS"])
    tcp = socket.socket(fileno=ports[0]["fd"])
    udp = socket.socket(fileno=ports[1]["fd"])
    tcp.settimeout(10)
    udp.settimeout(10)
    Path(marker).write_text(json.dumps(ports))
    conn, _ = tcp.accept()
    with conn:
        conn.settimeout(5)
        conn.sendall(b"arm")
        while conn.recv(1024):
            pass
    payload, peer = udp.recvfrom(1024)
    udp.sendto(payload, peer)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--arm", default="bin/arm")
    parser.add_argument("--serve")
    opts = parser.parse_args()
    if opts.serve:
        serve(opts.serve)
        return
    binary = str(Path(opts.arm).resolve())
    root = Path(tempfile.mkdtemp(prefix="arm-port-cli-"))
    home = root / "state"
    processes = []
    run = None
    external = None
    completed = False

    def arm(*args, success=True):
        p = subprocess.run([binary, "--home", str(home), *args], capture_output=True, text=True, timeout=15)
        if success:
            assert p.returncode == 0, (args, p.returncode, p.stdout, p.stderr)
            return json.loads(p.stdout)
        assert p.returncode != 0, (args, p.stdout)

    def start():
        process = subprocess.Popen([binary, "--home", str(home), "serve", "--cpu", "1", "--memory", "1GiB", "--disk", "1GiB"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        processes.append(process)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            assert process.poll() is None, process.stderr.read().decode()
            try:
                if json.loads((home / "endpoint.json").read_text())["pid"] == process.pid:
                    return process
            except (FileNotFoundError, json.JSONDecodeError):
                pass
            time.sleep(.02)
        raise AssertionError("foreground daemon did not become ready")

    try:
        daemon = start()
        marker = root / "ready.json"
        run = subprocess.Popen([binary, "--home", str(home), "run", "--cpu", "0", "--port", "web=tcp4://127.0.0.1:0", "--port", "queries=udp4://127.0.0.1:0", "--ttl", "20s", "--reason", "compiled socket activation", "--json", "--", sys.executable, str(Path(__file__).resolve()), "--serve", str(marker)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if marker.exists() and marker.stat().st_size:
                break
            assert run.poll() is None, run.communicate()
            time.sleep(.02)
        else:
            raise AssertionError("inherited server did not become ready")
        ports = json.loads(marker.read_text())
        assert [p["fd"] for p in ports] == [4, 5], ports
        with socket.create_connection(("127.0.0.1", ports[0]["port"]), timeout=3) as tcp:
            payload = b""
            while len(payload) < 3:
                chunk = tcp.recv(3 - len(payload))
                assert chunk, "inherited listener closed before its response"
                payload += chunk
            assert payload == b"arm"
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
            udp.settimeout(3)
            udp.sendto(b"arm", ("127.0.0.1", ports[1]["port"]))
            assert udp.recv(1024) == b"arm"
        stdout, stderr = run.communicate(timeout=10)
        assert run.returncode == 0, (stdout, stderr)
        lease = json.loads(stdout)
        assert lease["state"] == "terminal" and all(p["state"] == "observed_available" for p in lease["ports"]), lease

        reserved = arm("ports", "reserve", "--port", "raw=udp4://127.0.0.1:0", "--port-mode", "cooperative", "--reason", "revoke a cooperative allocation")
        external = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        external.bind(("127.0.0.1", reserved["ports"][0]["port"]))
        assert arm("release", reserved["id"])["state"] == "needs_attention"
        arm("reconcile", reserved["id"], success=False)
        external.close()
        external = None
        assert arm("reconcile", reserved["id"])["state"] == "terminal"

        held = arm("ports", "reserve", "--port", "tcp4://127.0.0.1:0", "--reason", "recover a daemon-owned descriptor")
        daemon.kill()  # exact foreground process created by this fixture
        daemon.wait(timeout=5)
        daemon = start()
        recovered = arm("inspect", held["id"])
        assert recovered["state"] == "needs_attention" and recovered["ports"][0]["state"] == "unknown", recovered
        assert arm("reconcile", held["id"])["state"] == "terminal"
        rows = arm("ports", "list")
        assert len(rows) == 1 and rows[0]["accounting"] == "protected-infrastructure", rows
        arm("release", rows[0]["id"], success=False)
        arm("ports", "discover")
        arm("daemon", "stop")
        daemon.wait(timeout=5)
        completed = True
        print("Compiled ports: inherited TCP/UDP traffic, cooperative revocation, crash recovery, native inventory and protected backend passed")
    finally:
        if external:
            external.close()
        if run and run.poll() is None:
            run.send_signal(signal.SIGINT)
            try:
                run.communicate(timeout=8)
            except subprocess.TimeoutExpired:
                run.kill()
                run.communicate(timeout=5)
        for process in processes:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            if process.stderr:
                process.stderr.close()
        if completed:
            shutil.rmtree(root)
        else:
            print("Retained port test state:", root, file=sys.stderr)


if __name__ == "__main__":
    main()
