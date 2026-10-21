package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

func TestAgentMessageMeshCannotForwardNativeSenderOperations(t *testing.T) {
	server := meshFixture(t)
	source, err := server.Manager.RegisterContact(agents.Contact{ID: "mesh-cannot-source", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately supply all valid native selectors and broad approved peer
	// grants. They still cannot turn the closed mesh API into native RPC.
	request := manager.AgentMessageRequest{ID: source.ID, Session: source.Session, Registration: source.Registration, Key: "mesh-cannot-create", Audience: "operator", Targets: []agents.Contact{}, Body: "cannot impersonate local registered source"}
	raw, _ := json.Marshal(request)
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("a", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-all", "observe-own", "lease-own", "run", "message", "contact-read"}, OperatorInbox: true, Contacts: []string{source.ID}}}
	service := &meshService{owner: server}
	for _, operation := range []string{"agent_message", "agent_message_lookup"} {
		value, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: operation, Key: "peer-wire-key", Params: raw})
		var detail *model.Error
		if !errors.As(err, &detail) || detail.Code != "forbidden" || value != nil {
			t.Fatalf("mesh forwarded %s: %#v %v", operation, value, err)
		}
	}
	lookup, err := server.Manager.LookupAgentMessage(source.ID, source.Session, source.Registration, request.Key)
	if err != nil || lookup.Found {
		t.Fatalf("forbidden mesh changed operation %+v %v", lookup, err)
	}
}
