package server

import (
	"encoding/json"
	"getarm.org/internal/mesh"
	"testing"
)

func TestMeshCannotDispatchDatabaseMaintenance(t *testing.T) {
	s := meshFixture(t)
	peer := mesh.Peer{Grant: mesh.Grant{Permissions: []string{"observe-host", "observe-own", "observe-all", "lease-own", "run", "message", "contact-read"}, OperatorInbox: true}}
	service := &meshService{owner: s}
	for _, operation := range []string{"database_stats", "database_check", "database_backup"} {
		if _, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: operation, Params: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("local database maintenance exposed to peer", operation)
		}
	}
}
