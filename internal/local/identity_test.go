package local

import (
	"context"
	"errors"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountIdentityUsesEffectiveUIDAndIgnoresEnvironment(t *testing.T) {
	t.Setenv("USER", "arm-test-spoofed-username")
	t.Setenv("LOGNAME", "arm-test-spoofed-username")
	got := CurrentIdentity()
	if got.UID != strconv.Itoa(os.Geteuid()) || got.Username == "arm-test-spoofed-username" {
		t.Fatalf("identity trusted environment or wrong UID: %+v", got)
	}
}

func TestAccountLookupValidatesUIDAndDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *user.User
		err   error
		want  string
	}{
		{"valid", &user.User{Uid: "501", Username: "alice"}, nil, "alice"},
		{"different UID", &user.User{Uid: "502", Username: "bob"}, nil, ""},
		{"unknown", nil, errors.New("no account"), ""},
		{"nil", nil, nil, ""},
		{"empty", &user.User{Uid: "501"}, nil, ""},
		{"control", &user.User{Uid: "501", Username: "alice\x1b[31m"}, nil, ""},
		{"long", &user.User{Uid: "501", Username: strings.Repeat("a", 257)}, nil, ""},
		{"invalid UTF8", &user.User{Uid: "501", Username: string([]byte{0xff})}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := identityResolver{slots: make(chan struct{}, 1)}
			got := r.resolve(context.Background(), "501", func(uid string) (*user.User, error) {
				if uid != "501" {
					t.Errorf("looked up caller name rather than UID: %s", uid)
				}
				return tc.value, tc.err
			})
			if got.UID != "501" || got.Username != tc.want {
				t.Fatalf("unexpected identity: %+v", got)
			}
		})
	}
}

func TestAccountLookupTimeoutBoundsOutstandingNativeCalls(t *testing.T) {
	r := identityResolver{slots: make(chan struct{}, 1)}
	blocked, completed := make(chan struct{}), make(chan struct{})
	defer func() {
		close(blocked)
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Error("released lookup did not finish")
		}
	}()
	var calls atomic.Int32
	lookup := func(uid string) (*user.User, error) {
		calls.Add(1)
		<-blocked
		defer close(completed)
		return &user.User{Uid: uid, Username: "late"}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got := r.resolve(ctx, "501", lookup)
	if ctx.Err() == nil || got.Username != "" || got.UID != "501" {
		t.Fatalf("timeout lost fallback: %+v %v", got, ctx.Err())
	}
	for i := 0; i < 20; i++ {
		if got := r.resolve(context.Background(), "501", lookup); got.Username != "" {
			t.Fatal("outstanding lookup returned another result")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("spawned %d blocked lookups", calls.Load())
	}
}
