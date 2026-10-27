//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

import (
	"syscall"
	"testing"
	"time"
)

func TestUnixPrototypeDarwinSocketCreationUsesForkLock(t *testing.T) {
	done := make(chan error, 1)
	p := &UnixPrototype{}
	syscall.ForkLock.Lock()
	locked := true
	joined := false
	t.Cleanup(func() {
		if locked {
			syscall.ForkLock.Unlock()
		}
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("raw socket worker did not join")
			}
		}
		if p.setupRaw != nil {
			_ = p.setupRaw.Close()
		}
	})
	go func() { done <- p.openRawSocket() }()
	select {
	case err := <-done:
		joined = true
		t.Fatal("raw socket creation bypassed ForkLock", err)
	case <-time.After(25 * time.Millisecond):
	}
	syscall.ForkLock.Unlock()
	locked = false
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("raw socket creation did not resume after ForkLock release")
	}
}
