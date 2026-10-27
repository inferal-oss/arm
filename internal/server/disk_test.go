package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
)

func TestLiveDiskMeshFreshnessSummaryExcludesPrivateFilesystemDetails(t *testing.T) {
	total, available, ceiling := int64(1000), int64(200), int64(500)
	b := &model.DiskBudget{Observation: model.DiskAvailability{At: time.Now().UTC(), Total: &total, Available: &available, Scope: "private-scope-sentinel", Path: "/private/path-sentinel", Volume: "private-volume-sentinel", Pool: "private-pool-sentinel", Method: "private-method-sentinel", Detail: "private-error-sentinel", Quality: "reported"}, Known: true, Capacity: 200, Reserved: 100, Available: 100, Ceiling: &ceiling}
	raw, err := json.Marshal(meshDiskSummary(b))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"known":true`) || !strings.Contains(string(raw), `"ceiling_bytes":500`) {
		t.Fatal(string(raw))
	}
	if strings.Contains(string(raw), "sentinel") {
		t.Fatal("mesh leaked private disk detail", string(raw))
	}
	s := meshFixture(t)
	service := &meshService{owner: s}
	peer := mesh.Peer{Identity: mesh.Identity{Host: strings.Repeat("f", 32)}, Grant: mesh.Grant{Permissions: []string{"observe-host"}}}
	value, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "status"})
	if err != nil {
		t.Fatal(err)
	}
	summary := value.(map[string]any)
	disk, ok := summary["disk"].(map[string]any)
	if !ok || disk["known"] != true {
		t.Fatal(summary)
	}
	resources, err := service.dispatch(t.Context(), peer, mesh.Request{Operation: "resources"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resources.([]map[string]any) {
		if r["kind"] == "disk" {
			found = true
			if r["availability_known"] != true || r["observation_at"] == nil {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal(resources)
	}
}
