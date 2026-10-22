package sshservice

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSourceHistoryWorkerProcessChild(t *testing.T) {
	path := os.Getenv("ARM_TEST_HISTORY_WORKER")
	if path == "" {
		return
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	fmt.Println("locked")
	if _, e = io.ReadFull(os.Stdin, make([]byte, 1)); e != nil {
		t.Fatal(e)
	}
	os.Exit(0) // kernel release must suffice; no PID authority is recorded
}

func TestSourceHistoryWorkerCrossProcessReadOnlyAndExitRelease(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	q := sourceArchiveRequest(t, home, b, "old-archive")
	done, e := ArchiveSourceHistory(t.Context(), home, b, q)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSourceHistoryWorkerProcessChild$")
	cmd.Env = append(os.Environ(), "ARM_TEST_HISTORY_WORKER="+filepath.Join(home, SourceHistoryName, sourceHistoryWorkerLockName))
	input, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line, e := bufio.NewReader(output).ReadString('\n')
	if e != nil || line != "locked\n" {
		t.Fatal(line, e)
	}
	if _, e = ReadState(t.Context(), home, b); e != nil {
		t.Fatal("worker blocks old inspection", e)
	}
	if _, e = LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key); e != nil {
		t.Fatal("worker blocks old receipt", e)
	}
	if got, e := ReconcileSourceHistory(t.Context(), home, b, q.Key); e != nil || got.Revision != done.Revision {
		t.Fatal("completed exact retry waited for worker", e)
	}
	m, e := historyCanonicalMarker(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	c, e := openHistoryCatalog(t.Context(), home, b, *m, false, false, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.close()
	if _, e = acquireHistoryWorker(c); !errors.Is(e, ErrHistoryBusy) {
		t.Fatal("cross-process worker not excluded", e)
	}
	_, e = input.Write([]byte{1})
	if e != nil {
		t.Fatal(e)
	}
	input.Close()
	e = cmd.Wait()
	waited = true
	if e != nil {
		t.Fatal(e)
	}
	w, e := acquireHistoryWorker(c)
	if e != nil {
		t.Fatal("process exit did not release exact lock", e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
}
