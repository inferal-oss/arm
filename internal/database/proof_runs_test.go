package database_test

import (
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"path/filepath"
	"testing"
)

func TestRunProofObservationsAndLostReporterBlock(t *testing.T) {
	for _, finish := range []bool{false, true} {
		home := proofFixture(t, false)
		m, err := manager.Open(home, model.Resources{})
		if err != nil {
			t.Fatal(err)
		}
		r, err := m.CreateRun(model.RunCreateRequest{Key: "proof-run", Command: []string{"fixture"}, CWD: filepath.Dir(home), Reason: "inspect logs"})
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("log body")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: []byte(" not in proof")}); err != nil {
			t.Fatal(err)
		}
		if finish {
			if _, err = m.FinishRun(model.RunFinishRequest{ID: r.ID, Outcome: "completed"}); err != nil {
				t.Fatal(err)
			}
		}
		if err = m.Close(); err != nil {
			t.Fatal(err)
		}
		proof, err := prove(t, home)
		if err != nil {
			t.Fatal(err)
		}
		if !proof.Complete || proof.Settled != finish || proof.Tables["proxy_runs"] != 1 || proof.Tables["proxy_run_chunks"] != 1 {
			t.Fatal("wrong run proof", proof)
		}
		// Removing/altering chunks cannot turn an acknowledged log into an empty
		// settled record, even after the CLI reports completion.
		edit(t, home, `DELETE FROM proxy_run_chunks`)
		if _, err = prove(t, home); err == nil {
			t.Fatal("missing acknowledged log accepted")
		}
	}
}

func TestRunProofRefusesPartialAndAlteredSchema(t *testing.T) {
	for _, query := range []string{`DROP TABLE proxy_run_chunks`, `DROP TABLE proxy_run_chunks; CREATE TABLE proxy_run_chunks(run TEXT,offset INTEGER,data TEXT)`} {
		home := proofFixture(t, false)
		edit(t, home, query)
		if _, err := prove(t, home); err == nil {
			t.Fatal("unsupported proxy schema accepted")
		}
	}
}
