package guardian

import (
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestJournalSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g", "actions.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	a := model.GuardianAction{ID: "a", At: time.Now(), Step: model.GuardianFreeze, Outcome: model.GuardianApplied, Reversible: true}
	if err := j.Save(a, map[string]Priority{"1@s": {Background: true}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < journalKeep+20; i++ {
		j.Save(model.GuardianAction{ID: string(rune('A'+i%26)) + time.Now().Format("150405.000000000"), Outcome: model.GuardianPlanned}, nil)
	}
	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	un := j2.Unreverted()
	if len(un) != 1 || un[0].ID != "a" {
		t.Fatalf("unreverted %+v", un)
	}
	if _, p, _ := j2.Get("a"); !p["1@s"].Background {
		t.Fatal("priorities lost")
	}
	now := time.Now()
	a.RevertedAt = &now
	j2.Save(a, nil)
	j3, _ := OpenJournal(path)
	if len(j3.Unreverted()) != 0 {
		t.Fatal("revert not persisted")
	}
}
