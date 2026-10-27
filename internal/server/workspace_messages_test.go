package server

import (
	"context"
	"encoding/json"
	"testing"

	"getarm.org/internal/model"
)

func TestRoutedMessagesRemainNativeOnlyAndReadDoesNotInitialize(t *testing.T) {
	s := meshFixture(t)
	ops := []string{"workspace_message_send", "workspace_message_reply", "workspace_message_page", "workspace_message_lookup", "workspace_message_cancel", "workspace_messages_retire"}
	browser := context.WithValue(t.Context(), browserContextKey{}, true)
	for _, op := range ops {
		_, e := s.dispatch(browser, model.WireRequest{Operation: op, Params: json.RawMessage(`{}`)})
		if e == nil {
			t.Fatal("browser reached routed native authority", op)
		}
	}
	before, e := s.Manager.WorkspaceRoutingRead(t.Context())
	if e != nil || before.Initialized {
		t.Fatal("native read created routing authority", before, e)
	}
}
