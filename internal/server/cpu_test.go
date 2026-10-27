package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestLiveCPUMeshSummaryRetainsDeficitWithoutPrivateEvidence(t *testing.T) {
	cores, capacity, busy, available := 4, 2.0, 3.0, 0.0
	b := &model.CPUBudget{Observation: model.CPUAvailability{
		At: time.Now().UTC(), EligibleCPUs: &cores, Capacity: &capacity, Busy: &busy, Available: &available,
		Scope: "private-affinity-sentinel", Method: "private-method-sentinel", Detail: "private-cgroup-sentinel", Quality: "reported",
	}, Capacity: 0, Reserved: 1, Available: 0, Deficit: 1, Known: false, Stale: true, StaleAfter: 6 * time.Second}
	raw, err := json.Marshal(meshCPUSummary(b))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"known":false`, `"stale":true`, `"deficit_cores":1`, `"reserved_cores":1`, `"available_cores":0`} {
		if !strings.Contains(string(raw), fragment) {
			t.Fatal("mesh lost retained promises or freshness", string(raw))
		}
	}
	if strings.Contains(string(raw), "sentinel") {
		t.Fatal("mesh leaked private CPU evidence", string(raw))
	}
	if meshCPUSummary(nil) != nil {
		t.Fatal("missing target budget manufactured a CPU summary")
	}
}
