package server

import (
	"errors"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"strings"
	"testing"
)

func TestNodeUninstallRequiresBothCurrentDirectGrants(t *testing.T) {
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("1", 32)}, State: "active", Proven: true, Pair: strings.Repeat("2", 64), Revision: 1}
	identity := mesh.Identity{Host: strings.Repeat("3", 32)}
	request := mesh.Request{Host: identity.Host, Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: 1}, Operation: "node_uninstall_plan"}
	service := &meshService{owner: &Server{}}
	for _, permissions := range [][]string{nil, {"node-stop"}, {"node-uninstall"}} {
		peer.Grant.Permissions = permissions
		_, e := service.dispatchUninstall(t.Context(), peer, request, identity, nil)
		var api *model.Error
		if !errors.As(e, &api) || api.Code != "forbidden" {
			t.Fatal("missing independent grant reached planning", permissions, e)
		}
	}
	peer.Grant.Permissions = []string{"node-stop", "node-uninstall"}
	request.Expected.Revision++
	if _, e := service.dispatchUninstall(t.Context(), peer, request, identity, nil); e == nil {
		t.Fatal("stale direct enrollment accepted")
	}
}
