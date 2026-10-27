package server

import (
	"getarm.org/internal/model"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewHostilePeerOverflowPreservesOtherReports(t *testing.T) {
	report := model.PeerOverview{TotalsComplete: true, Instances: []model.PeerInstance{
		{Host: "legitimate", Snapshot: &model.PeerSnapshot{Held: model.Resources{Memory: 10}, HeldCount: 1, Items: []model.PeerItem{{State: "active", Resources: model.Resources{Memory: 10}}}}},
		{Host: "overflow", Snapshot: &model.PeerSnapshot{Held: model.Resources{Memory: math.MaxInt64}, HeldCount: 1, Items: []model.PeerItem{{State: "active", Resources: model.Resources{Memory: math.MaxInt64}}}}},
	}}
	if err := accumulatePeerTotals(&report); err != nil {
		t.Fatal("one peer poisoned entire advisory picture", err)
	}
	if report.PeerHeld.Memory != 10 || report.TotalsComplete || report.Instances[1].Snapshot != nil || report.Instances[1].Error == "" {
		t.Fatalf("hostile totals not isolated: %+v", report)
	}
}
func TestReviewPeerUntruncatedRowsMatchReportedCounts(t *testing.T) {
	if validPeerSnapshot(model.PeerSnapshot{Held: model.Resources{Memory: 1}, HeldCount: 1, Items: []model.PeerItem{}}) {
		t.Fatal("complete itemized report silently omitted its claimed lease")
	}
}

func TestPeerPollModuleEmbeddedAndServed(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	request := httptest.NewRequest("GET", "http://"+s.authority+"/peer-poll.mjs", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "createPeerPollGate") {
		t.Fatalf("new dashboard import is not served: status=%d", response.Code)
	}
}
