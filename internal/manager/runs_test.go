package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func proxyRequest(t *testing.T, key string) model.RunCreateRequest {
	t.Helper()
	return model.RunCreateRequest{Key: key, Command: []string{"fixture", "--private"}, CWD: t.TempDir(), Reason: "observe owned fixture"}
}

func TestRunProxyAcknowledgementsAndSnapshotPages(t *testing.T) {
	m := newManager(t)
	q := proxyRequest(t, "pages")
	r, err := m.CreateRun(q)
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.CreateRun(q)
	if err != nil || same.ID != r.ID {
		t.Fatal("create receipt lost", same, err)
	}
	q.Reason = "different"
	if _, err = m.CreateRun(q); err == nil {
		t.Fatal("changed key accepted")
	}
	first := bytes.Repeat([]byte{'a'}, runPageBytes)
	second := []byte("tail\xff\x00")
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: first})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: first})
	if err != nil || replay.AcceptedBytes != int64(len(first)) {
		t.Fatal("duplicate bytes", err, replay)
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("different")}); err == nil {
		t.Fatal("changed append accepted")
	}
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: second})
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.FinishRun(model.RunFinishRequest{ID: r.ID, ExitCode: 7, Outcome: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := m.RunOutput(r.Name, 0)
	if err != nil || !page.Done || page.NextOffset != runPageBytes || !bytes.Equal(page.Data, first) {
		t.Fatal("first page", page, err)
	}
	page, err = m.RunOutput(r.ID, page.NextOffset)
	if err != nil || !page.Done || !bytes.Equal(page.Data, second) || page.NextOffset != r.LogBytes {
		t.Fatal("tail page", page, err)
	}
	if _, err = m.RunOutput(r.ID, -1); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err = m.RunOutput(r.ID, r.LogBytes+1); err == nil {
		t.Fatal("past EOF offset accepted")
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: []byte("late")}); err == nil {
		t.Fatal("late append accepted")
	}
	if _, err = m.FinishRun(model.RunFinishRequest{ID: r.ID, ExitCode: 7, Outcome: "failed"}); err != nil {
		t.Fatal("final receipt not idempotent", err)
	}
	if _, err = m.FinishRun(model.RunFinishRequest{ID: r.ID, Outcome: "completed"}); err == nil {
		t.Fatal("changed final result accepted")
	}
	status, err := m.Status()
	if err != nil || len(status.Leases) != 0 || status.Reserved.CPU != 0 {
		t.Fatal("proxy created resource claim", status, err)
	}
}

func TestRunProxyHeartbeatAndReporterLoss(t *testing.T) {
	m := newManager(t)
	r, err := m.CreateRun(proxyRequest(t, "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("first")})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.proxyReported[r.ID] = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	heartbeat, err := m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes})
	if err != nil || heartbeat.AcceptedBytes != r.AcceptedBytes {
		t.Fatal("heartbeat altered offset", heartbeat, err)
	}
	// Heartbeat must preserve the exact last data receipt.
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("first")}); err != nil {
		t.Fatal("heartbeat lost chunk receipt", err)
	}
	m.mu.Lock()
	err = m.expireProxyRuns(time.Now().Add(31 * time.Second))
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	lost, err := m.InspectRun(r.ID)
	if err != nil || lost.State != "interrupted" || lost.Outcome != "reporter_lost" || lost.Ended != nil || lost.ExitCode != nil {
		t.Fatal("loss asserted exit", lost, err)
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes}); err == nil {
		t.Fatal("lost reporter revived")
	}
	if _, err = m.FinishRun(model.RunFinishRequest{ID: r.ID, Outcome: "completed"}); err == nil {
		t.Fatal("lost reporter supplied terminal witness")
	}
	page, err := m.RunOutput(r.ID, 0)
	if err != nil || !page.Done || string(page.Data) != "first" {
		t.Fatal("lost log unavailable", page, err)
	}
}

func TestRunProxyRestartPreservesLogWithoutReplaying(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.CreateRun(proxyRequest(t, "restart"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("saved")}); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	page, err := m.RunOutput(r.Name, 0)
	if err != nil || string(page.Data) != "saved" || page.Run.State != "interrupted" || page.Run.Ended != nil || page.Run.ExitCode != nil {
		t.Fatal(page, err)
	}
	status, err := m.Status()
	if err != nil || len(status.Leases) != 0 {
		t.Fatal("restart created execution", status, err)
	}
}

func TestRunProxyRetentionBoundaryAcknowledgesDiscardedBytes(t *testing.T) {
	m := newManager(t)
	r, err := m.CreateRun(proxyRequest(t, "retention"))
	if err != nil {
		t.Fatal(err)
	}
	// A metadata boundary fixture models an already-retained prefix. It does not
	// claim a physical 64MiB data-path acceptance run.
	m.mu.Lock()
	rec, err := proxyRecord(m.db, r.ID)
	if err == nil {
		rec.LogBytes = proxyLogLimit - 1
		rec.AcceptedBytes = proxyLogLimit - 1
		raw, _ := json.Marshal(rec)
		_, err = m.db.Exec(`UPDATE proxy_runs SET data=? WHERE id=?`, string(raw), r.ID)
		if err == nil {
			// Only the final preexisting byte is materialized in this boundary fixture.
			_, err = m.db.Exec(`INSERT INTO proxy_run_chunks(run,offset,data) VALUES(?,?,?)`, r.ID, proxyLogLimit-2, "cA==")
		}
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: proxyLogLimit - 1, Data: []byte("xy")})
	if err != nil || r.LogBytes != proxyLogLimit || r.AcceptedBytes != proxyLogLimit+1 || !r.Truncated {
		t.Fatal(r, err)
	}
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: []byte("later")})
	if err != nil || r.LogBytes != proxyLogLimit || r.AcceptedBytes != proxyLogLimit+6 {
		t.Fatal("spliced after truncation", r, err)
	}
	var raw string
	if err = m.db.QueryRow(`SELECT data FROM proxy_run_chunks WHERE run=?`, r.ID).Scan(&raw); err != nil || raw != "cHg=" {
		t.Fatal("retained suffix not exact", raw, err)
	}
}

func TestRunLeasedInventoryAndLogPathRefusal(t *testing.T) {
	m := newManager(t)
	// Seed a terminal historical lease record so this inventory test needs no
	// host CPU launch authority and never executes the saved command.
	q := RunRequest{Request: request("history"), Command: []string{"fixture", "test"}, CWD: t.TempDir()}
	l := model.Lease{ID: "11111111111111111111111111111111", Host: m.host, Request: q.Request, State: "terminal", Outcome: "exited", Created: time.Now().UTC()}
	raw, _ := json.Marshal(l)
	saved, _ := json.Marshal(q)
	if _, err := m.db.Exec(`INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,?)`, l.ID, q.Key, string(raw), string(saved)); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(m.home, "logs")
	if err := os.Mkdir(logs, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, l.ID+".log")
	if err := os.WriteFile(path, []byte("leased output"), 0600); err != nil {
		t.Fatal(err)
	}
	runs, err := m.Runs()
	if err != nil || len(runs) != 1 || runs[0].LeaseID != l.ID || runs[0].Reason != q.Reason {
		t.Fatal(runs, err)
	}
	page, err := m.RunOutput(runs[0].Name, 0)
	if err != nil || string(page.Data) != "leased output" || !page.Done {
		t.Fatal(page, err)
	}
	if _, err = m.RunOutput("../../outside", 0); err == nil {
		t.Fatal("path selector accepted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RunOutput(l.ID, 0); err == nil {
		t.Fatal("followed log symlink")
	}
}

func TestRunProxyInputAndChunkBounds(t *testing.T) {
	m := newManager(t)
	q := proxyRequest(t, "input")
	q.Agent = "missing"
	if _, err := m.CreateRun(q); err == nil {
		t.Fatal("unknown agent accepted")
	}
	q.Agent = ""
	q.CWD = "relative"
	if _, err := m.CreateRun(q); err == nil {
		t.Fatal("relative cwd accepted")
	}
	q.CWD = t.TempDir()
	q.Reason = ""
	if _, err := m.CreateRun(q); err == nil {
		t.Fatal("missing reason accepted")
	}
	q.Reason = "bounds"
	r, err := m.CreateRun(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, appendQ := range []model.RunAppendRequest{{ID: r.ID, Offset: -1}, {ID: r.ID, Data: make([]byte, runPageBytes+1)}, {ID: r.ID, Offset: 1}, {ID: "../outside"}} {
		if _, err = m.AppendRun(appendQ); err == nil {
			t.Fatal("invalid append accepted", appendQ.Offset, len(appendQ.Data))
		}
	}
}

func TestRunCanonicalLookupSurvivesInventoryBound(t *testing.T) {
	m := newManager(t)
	r, err := m.CreateRun(proxyRequest(t, "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: []byte("still readable")}); err != nil {
		t.Fatal(err)
	}
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runInventoryLimit; i++ {
		id := fmt.Sprintf("%032x", i+1)
		rec := proxyRunRecord{Run: model.Run{ID: id, Created: time.Now().UTC()}, LastOffset: -1}
		raw, _ := json.Marshal(rec)
		if _, err = tx.Exec(`INSERT INTO proxy_runs(id,request_key,request,data) VALUES(?,?,?,?)`, id, "fixture-"+id, `{}`, string(raw)); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Runs(); err == nil {
		t.Fatal("oversized inventory silently truncated")
	}
	page, err := m.RunOutput(r.ID, 0)
	if err != nil || string(page.Data) != "still readable" {
		t.Fatal("canonical lookup depends on inventory", page, err)
	}
}

func TestRunNativeExecutionObservation(t *testing.T) {
	m := newManager(t)
	q := request("native-run")
	run := RunRequest{Request: q, Command: []string{"/bin/sh", "-c", "printf 'actual output'; exit 3"}, CWD: t.TempDir()}
	l, err := m.Acquire(q, &run)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { current, e := m.Inspect(l.ID); return e == nil && current.State == "terminal" })
	page, err := m.RunOutput(l.ID, 0)
	if err != nil || page.Run.LeaseID != l.ID || page.Run.Command[0] != "/bin/sh" || page.Run.Reason != q.Reason || !page.Done || string(page.Data) != "actual output" || page.Run.ExitCode == nil || *page.Run.ExitCode != 3 {
		t.Fatal("native run observation", page, err)
	}
}

func TestRunProxyCompactsMoreThan4096SmallAppends(t *testing.T) {
	m := newManager(t)
	r, err := m.CreateRun(proxyRequest(t, "small-appends"))
	if err != nil {
		t.Fatal(err)
	}
	var expected []byte
	for i := 0; i < 4097; i++ {
		data := []byte(fmt.Sprintf("line %04d\n", i))
		r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: data})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		expected = append(expected, data...)
	}
	if r.Truncated || r.LogBytes != int64(len(expected)) || r.AcceptedBytes != int64(len(expected)) {
		t.Fatal("small appends truncated", r)
	}
	var chunks int
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM proxy_run_chunks WHERE run=?`, r.ID).Scan(&chunks); err != nil || chunks != 1 {
		t.Fatal("small appends were not packed", chunks, err)
	}
	page, err := m.RunOutput(r.ID, 0)
	if err != nil || !bytes.Equal(page.Data, expected) {
		t.Fatal("packed log differs", len(page.Data), err)
	}
	// A heartbeat and exact retry of the last input must not repeat packed bytes.
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes}); err != nil {
		t.Fatal(err)
	}
	last := []byte("line 4096\n")
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes - int64(len(last)), Data: last})
	if err != nil || r.LogBytes != int64(len(expected)) {
		t.Fatal("packed retry changed log", r, err)
	}
}

func TestRunProxyCompactionSplitsTailAtPageBoundary(t *testing.T) {
	m := newManager(t)
	r, err := m.CreateRun(proxyRequest(t, "split-tail"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Repeat([]byte{'x'}, runPageBytes-3)
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Data: prefix})
	if err != nil {
		t.Fatal(err)
	}
	suffix := []byte("0123456789")
	r, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: r.AcceptedBytes, Data: suffix})
	if err != nil {
		t.Fatal(err)
	}
	var chunks int
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM proxy_run_chunks WHERE run=?`, r.ID).Scan(&chunks); err != nil || chunks != 2 {
		t.Fatal("tail not split", chunks, err)
	}
	page, err := m.RunOutput(r.ID, runPageBytes-6)
	if err != nil || string(page.Data) != "xxx0123456789" || page.NextOffset != r.LogBytes {
		t.Fatal("boundary read differs", page, err)
	}
	first, err := m.RunOutput(r.ID, 0)
	if err != nil || len(first.Data) != runPageBytes || !bytes.Equal(first.Data, append(append([]byte{}, prefix...), suffix[:3]...)) {
		t.Fatal("first page differs", len(first.Data), err)
	}
	if _, err = m.AppendRun(model.RunAppendRequest{ID: r.ID, Offset: int64(len(prefix)), Data: suffix}); err != nil {
		t.Fatal("split retry lost receipt", err)
	}
}
