package leaseclock

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestDeadlineDiscontinuitiesAndSerialization(t *testing.T) {
	s := Sample{Boot: "boot-a", Tick: 123, Wall: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	d, err := New(s, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored Deadline
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	for _, jump := range []time.Duration{-24 * time.Hour, 0, 24 * time.Hour} {
		now := s
		now.Wall = now.Wall.Add(jump)
		now.Tick += int64(time.Second) - 1
		n, err := restored.Remaining(now, &d.Wall)
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
		now.Tick++
		if n, err = restored.Remaining(now, &d.Wall); n != 0 || err != nil {
			t.Fatal(n, err)
		}
	}
	for _, kind := range []string{"other-boot", "regressed", "missing-wall", "changed-wall", "version", "zero-wall", "missing"} {
		t.Run(kind, func(t *testing.T) {
			now, bad := s, *d
			wall := &d.Wall
			switch kind {
			case "other-boot":
				now.Boot = "boot-b"
			case "regressed":
				now.Tick--
			case "missing-wall":
				wall = nil
			case "changed-wall":
				x := d.Wall.Add(time.Second)
				wall = &x
			case "version":
				bad.Version++
			case "zero-wall":
				bad.Wall = time.Time{}
				wall = &bad.Wall
			case "missing":
				if _, err := (*Deadline)(nil).Remaining(now, wall); err == nil {
					t.Fatal("nil accepted")
				}
				return
			}
			if _, err := bad.Remaining(now, wall); err == nil {
				t.Fatal("uncertain accepted")
			}
		})
	}
}

func TestClockArithmeticBounds(t *testing.T) {
	s := Sample{Boot: "boot", Tick: 1, Wall: time.Now().UTC()}
	for _, ttl := range []time.Duration{0, -1, 8 * 24 * time.Hour} {
		if _, err := New(s, ttl); err == nil {
			t.Fatal(ttl)
		}
	}
	s.Wall = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if _, err := New(s, time.Second); err == nil {
		t.Fatal("unpersistable wall deadline accepted")
	}
	s.Wall = time.Now().UTC()
	s.Tick = math.MaxInt64
	if _, err := New(s, time.Second); err == nil {
		t.Fatal("overflow accepted")
	}
	for _, x := range [][2]int64{{-1, 0}, {0, -1}, {0, 1e9}, {math.MaxInt64, 0}} {
		if _, err := tick(x[0], x[1]); err == nil {
			t.Fatal(x)
		}
	}
	if n, err := tick(1, 2); err != nil || n != 1000000002 {
		t.Fatal(n, err)
	}
}

// The child stops only itself; the parent retains its original handle and Wait.
// This is process suspension evidence, not a claim that the host slept.
func TestNativeClockSuspendedProcess(t *testing.T) {
	if os.Getenv("ARM_LEASE_CLOCK_CHILD") == "1" {
		a, err := Read()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(a)
		os.Stdout.Write(append(raw, '\n'))
		if err = syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		b, err := Read()
		if err != nil {
			t.Fatal(err)
		}
		if b.Boot != a.Boot || b.Tick-a.Tick < int64(100*time.Millisecond) {
			t.Fatal(a, b)
		}
		return
	}
	a, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeClockSuspendedProcess$")
	cmd.Env = append(os.Environ(), "ARM_LEASE_CLOCK_CHILD=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			contErr := cmd.Process.Signal(syscall.SIGCONT)
			killErr := cmd.Process.Kill()
			waitErr := cmd.Wait()
			var exited *exec.ExitError
			if waitErr != nil && !errors.As(waitErr, &exited) {
				t.Errorf("original child join uncertain: %v", waitErr)
			}
			if contErr != nil || killErr != nil {
				t.Errorf("original child cleanup signals: CONT=%v KILL=%v", contErr, killErr)
			}
		}
	}()
	var b Sample
	if err = json.NewDecoder(out).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.Boot != a.Boot || b.Tick < a.Tick {
		t.Fatal(a, b)
	}
	until := time.Now().Add(time.Second)
	for {
		stopped, err := observeStopped(cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if stopped {
			break
		}
		if time.Now().After(until) {
			t.Fatal("owned child did not reach native stopped state")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(120 * time.Millisecond)
	if err = cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	// Drain the Go test's ordinary PASS output before its single terminal Wait.
	var buf [1024]byte
	for {
		_, err = out.Read(buf[:])
		if err != nil {
			break
		}
	}
	err = cmd.Wait()
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil || c.Boot != a.Boot || c.Tick-b.Tick < int64(100*time.Millisecond) {
		t.Fatal(a, b, c, err)
	}
	t.Logf("native boot scope %s; child %d joined; elapsed %s", c.Boot, cmd.Process.Pid, time.Duration(c.Tick-b.Tick))
}
