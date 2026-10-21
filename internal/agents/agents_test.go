package agents

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

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
