package tui

import (
	"getarm.org/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRunDetailsHostIdentityAndZeroTimes(t *testing.T) {
	m, _, _ := runFixture(t, nil)
	local, remote := strings.Repeat("a", 32), strings.Repeat("b", 32)
	m.data.status.Host = local
	m.data.names = renderLabels{authority: local, hostName: "Build station"}
	r := model.Run{ID: "synthetic-run", Name: "Readable run", Host: local, Reason: "Compile preview", State: "active", Command: []string{"tool", "build"}}
	m.runsUI = &runsDashboard{selected: &r}
	text := m.runsText()
	if !strings.Contains(text, "Build station · Local") || strings.Contains(text, "0001") || !strings.Contains(text, "Last report: Not reported") || !strings.Contains(text, "[i] Exact record") {
		t.Fatal(text)
	}
	r.Host = remote
	if strings.Contains(m.runsText(), "· Local") {
		t.Fatal("remote host marked local")
	}
	r.Host = ""
	if !strings.Contains(m.runsText(), "Unknown host") || strings.Contains(m.runsText(), "· Local") {
		t.Fatal("missing host guessed")
	}
	zero := time.Time{}
	if runDetailDate(&zero) != "Not reported" {
		t.Fatal("year-one date displayed")
	}
}

func TestAssociatedLeaseHostUsesSameVerifiedLocalIdentity(t *testing.T) {
	m, _ := fixtureDashboard(t)
	local, remote := strings.Repeat("a", 32), strings.Repeat("b", 32)
	m.data.status.Host = local
	m.data.names = renderLabels{authority: local, hostName: "Build station"}
	l := activeLease("sample")
	l.Host = local
	m.detail = &l
	if !strings.Contains(m.leaseDetailText(), "Build station · Local") {
		t.Fatal(m.leaseDetailText())
	}
	l.Host = remote
	if strings.Contains(m.leaseDetailText(), "· Local") {
		t.Fatal("remote lease marked local")
	}
}

func TestUnfamiliarRunStateNeverInheritsFinishedOutcome(t *testing.T) {
	for _, state := range []string{"future_state", "constructor", "__proto__"} {
		for _, outcome := range []string{"expired", "released", "cancelled", "rejected", "failed", "completed"} {
			r := model.Run{State: state, Outcome: outcome}
			if runStatusLabel(r) != "Unknown" || !currentRunRecord(r) {
				t.Fatal("uncertain run presented as finished", r)
			}
		}
	}
}
