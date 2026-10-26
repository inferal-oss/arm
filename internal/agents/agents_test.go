package agents

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

func TestNativeStartFenceDeniesCommandBeforeLaunch(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	script := []byte("#!/bin/sh\nprintf launched > " + strconv.Quote(marker) + "\n")
	if err := os.WriteFile(filepath.Join(dir, "codex"), script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := Contact{ID: "fixture", Adapter: "codex", Session: uuid.NewString(), CWD: dir, Delivery: "native", Registration: "test-registration"}
	called := false
	d := DeliverWithStartFence(context.Background(), c, "warning", func(start func() error) (bool, error) {
		called = true
		return false, nil
	})
	if !called || d.State != "superseded" {
		t.Fatalf("denied start was not reported: called=%t delivery=%+v", called, d)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("denied native command ran: %v", err)
	}
	d = DeliverWithStartFence(context.Background(), c, "warning", func(start func() error) (bool, error) {
		return true, start()
	})
	if d.State != "queued" {
		t.Fatalf("allowed native command did not complete: %+v", d)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("allowed command did not run: %v", err)
	}
}

func TestUnsafeOrAmbiguousContactsAreRejected(t *testing.T) {
	c := Contact{ID: "fixture", Adapter: "claude", Session: uuid.NewString(), CWD: t.TempDir()}
	if c.Validate() == nil {
		t.Fatal("accepted Claude live injection without declared idle state")
	}
	c.Idle = true
	c.Session = "--last"
	if c.Validate() == nil {
		t.Fatal("accepted ambiguous session selector")
	}
	c.Session = uuid.NewString()
	c.Remote = "unix:///tmp/fixture"
	c.ID = "broadcast"
	if c.Validate() == nil {
		t.Fatal("accepted reserved audience name")
	}
}

func TestUnknownClaudeSessionIsNotCreated(t *testing.T) {
	c := Contact{ID: "fixture", Adapter: "claude", Session: uuid.NewString(), CWD: t.TempDir(), ConfigDir: t.TempDir(), Idle: true}
	d := Deliver(context.Background(), c, "fixture")
	if d.State != "failed" {
		t.Fatal("accepted delivery to unknown Claude session")
	}
}

func TestPollingDoesNotRequireAnIdleCLIOrLaunchAnotherConversation(t *testing.T) {
	for _, adapter := range []string{"claude", "codex", "generic"} {
		c := Contact{ID: "poll-fixture", Adapter: adapter, Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "poll"}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		d := Deliver(t.Context(), c, "only the inbox should receive this")
		if d.State != "available" || d.Mode != "poll" || d.Reply != "" {
			t.Fatalf("poll claimed native delivery: %+v", d)
		}
	}
	c := Contact{ID: "poll-fixture", Adapter: "generic", Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "native"}
	if c.Validate() == nil {
		t.Fatal("generic adapter claimed native support")
	}
	c.Delivery = "typo"
	if c.Validate() == nil {
		t.Fatal("accepted unknown delivery mode")
	}
}
