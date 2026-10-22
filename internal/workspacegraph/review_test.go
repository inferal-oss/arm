package workspacegraph

import (
	"math"
	"testing"
	"time"
)

func TestPageRejectsOverflowingOffsetBeforeLengthAddition(t *testing.T) {
	scope, reports, boot := graphFixture(t, 2, [][2]int{{1, 2}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	pages, e := Pages(s, MaxPageBytes)
	if e != nil {
		t.Fatal(e)
	}
	p := pages[0]
	p.NodeTotal = 1
	p.NodeOffset = math.MaxInt
	p.Snapshot.Nodes = p.Snapshot.Nodes[:1]
	p.Snapshot.Edges = []Edge{}
	p.EdgeTotal = 0
	if e = p.Validate(); e == nil {
		t.Fatal("overflowing node offset accepted")
	}
	p = pages[0]
	p.EdgeOffset = math.MaxInt
	if e = p.Validate(); e == nil {
		t.Fatal("overflowing edge offset accepted")
	}
}
func TestHistoricalGraphEvidenceRejectsUnencodableTimestamps(t *testing.T) {
	scope, reports, boot := graphFixture(t, 2, [][2]int{{1, 2}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	future := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Issued = future
	s.ValidUntil = future.Add(time.Minute)
	s.Hash = s.Digest()
	if s.Hash != "" {
		t.Fatal("unencodable snapshot fabricated a digest")
	}
	if e = s.ValidateStructure(); e == nil {
		t.Fatal("unencodable snapshot accepted")
	}
	r := reports[0].Report
	r.Issued = future
	r.ValidUntil = future.Add(time.Minute)
	if e = r.ValidateStructure(); e == nil {
		t.Fatal("unencodable report accepted")
	}
	if reportHash(r) != "" {
		t.Fatal("unencodable report fabricated a digest")
	}
}
func TestBuildRejectsUnboundedScopeBeforeCopyAndSort(t *testing.T) {
	scope, reports, boot := graphFixture(t, 2, [][2]int{{1, 2}})
	scope.Nodes = make([]Node, MaxNodes+1)
	if _, e := Build(scope, reports, boot, 0); e == nil {
		t.Fatal("oversized scope accepted")
	}
}
