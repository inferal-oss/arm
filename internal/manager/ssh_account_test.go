package manager

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSSHAccountLookupWaitCanCancelWithoutFreeSlot(t *testing.T) {
	sshAccountSlot <- struct{}{}
	defer func() { <-sshAccountSlot }()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := resolveSSHAccount(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatal("cancel waited for occupied native lookup")
	}
}
func TestSSHAccountConcurrentNativeLookupsSerializeWithoutFalseRefusal(t *testing.T) {
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, e := resolveSSHAccount(t.Context())
			if e != nil {
				failures <- e
				return
			}
			if u.Uid != strconv.Itoa(os.Geteuid()) || u.Username == "" || u.HomeDir == "" {
				failures <- errors.New("native account identity changed")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
}
