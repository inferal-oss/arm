package database

import (
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
	"strings"
	"testing"
)

func sourceProofPair() (model.Lease, model.Lease) {
	s := kubesource.Scope{Operation: strings.Repeat("a", 64), Attempt: strings.Repeat("b", 32), Stage: kubesource.Inspect, B: 1}
	host := strings.Repeat("c", 32)
	source := model.Lease{ID: strings.Repeat("1", 32), Host: host, Owner: "501", State: "terminal", Request: s.Request(host, "", false)}
	source.Request.Kind = "relay-transit"
	parent := model.Lease{ID: strings.Repeat("2", 32), Host: host, Owner: "501", State: "needs_attention", Request: s.Request(host, "", true), ManagedDir: "owned", Artifact: &model.Artifact{Name: strings.Repeat("2", 32), Identity: &model.FileIdentity{Owner: 501, BirthKnown: true, Inode: 1}, ContentObserved: true}}
	source.Request.StorageLease = parent.ID
	return source, parent
}
func TestKubernetesSourceProofClosedRelationship(t *testing.T) {
	source, parent := sourceProofPair()
	if err := validateKubernetesSourcePairs(map[string]model.Lease{source.ID: source, parent.ID: parent}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing-parent", "wrong-owner", "obsolete-inspect-three", "wrong-attempt", "duplicate-parent", "extra-consumer", "unattached-written-parent"} {
		t.Run(name, func(t *testing.T) {
			s, p := source, parent
			rows := map[string]model.Lease{s.ID: s, p.ID: p}
			switch name {
			case "missing-parent":
				delete(rows, p.ID)
			case "wrong-owner":
				p.Owner = "502"
				rows[p.ID] = p
			case "obsolete-inspect-three":
				p.Request.Resources.Disk = 262144
				rows[p.ID] = p
			case "wrong-attempt":
				p.Request.Key = strings.Replace(p.Request.Key, strings.Repeat("b", 32), strings.Repeat("d", 32), 1)
				rows[p.ID] = p
			case "duplicate-parent":
				other := p
				other.ID = strings.Repeat("3", 32)
				rows[other.ID] = other
			case "extra-consumer":
				rows["foreign"] = model.Lease{Request: model.Request{StorageLease: p.ID}}
			case "unattached-written-parent":
				s.Request.StorageLease = ""
				rows[s.ID] = s
			}
			if validateKubernetesSourcePairs(rows) == nil {
				t.Fatal("noncanonical source relationship accepted")
			}
		})
	}
}
func TestKubernetesSourceProofPartialAdmissionIsNotAdoption(t *testing.T) {
	source, _ := sourceProofPair()
	source.State = "active"
	source.Request.StorageLease = ""
	if err := validateKubernetesSourcePairs(map[string]model.Lease{source.ID: source}); err != nil {
		t.Fatal("valid partial source should retain the normal unsettled blocker", err)
	}
}

func TestKubernetesSourceProofUnattachedParentRejectsForeignReferences(t *testing.T) {
	for _, tc := range []struct {
		name       string
		references int
	}{
		{name: "empty-partial-parent", references: 0},
		{name: "one-foreign-reference", references: 1},
		{name: "two-foreign-references", references: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, parent := sourceProofPair()
			source.State = "active"
			source.Request.StorageLease = ""
			parent.State = "active"
			artifact := *parent.Artifact
			artifact.ContentObserved = false
			parent.Artifact = &artifact
			rows := map[string]model.Lease{source.ID: source, parent.ID: parent}
			for i := 0; i < tc.references; i++ {
				id := strings.Repeat(string(rune('3'+i)), 32)
				rows[id] = model.Lease{ID: id, Host: parent.Host, Owner: parent.Owner, State: "terminal", Request: model.Request{StorageLease: parent.ID}}
			}
			err := validateKubernetesSourcePairs(rows)
			if tc.references == 0 && err != nil {
				t.Fatal("valid empty partial parent refused", err)
			}
			if tc.references > 0 && err == nil {
				t.Fatal("foreign ordinary terminal consumers accepted on unattached source parent")
			}
		})
	}
}
