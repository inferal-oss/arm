package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Subprocess exits deliberately bypass defer/SQLite rollback. Only disposable
// test SQLite files/public fixture identities cross the process boundary.
func TestWorkspaceCrashBoundaryChild(t *testing.T) {
	mode := os.Getenv("ARM_WORKSPACE_CRASH_MODE")
	if mode == "" {
		return
	}
	var self Identity
	var authority Authority
	var wall time.Time
	if json.Unmarshal([]byte(os.Getenv("ARM_WORKSPACE_SELF")), &self) != nil || json.Unmarshal([]byte(os.Getenv("ARM_WORKSPACE_AUTHORITY")), &authority) != nil || wall.UnmarshalText([]byte(os.Getenv("ARM_WORKSPACE_WALL"))) != nil {
		t.Fatal("invalid child fixture")
	}
	db, e := sql.Open("sqlite", os.Getenv("ARM_WORKSPACE_DB"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA journal_mode=WAL;PRAGMA busy_timeout=5000`); e != nil {
		t.Fatal(e)
	}
	clock := &fakeClock{wall: wall}
	ctx := context.Background()
	s, e := Open(ctx, db, authority, self, clock)
	if e != nil {
		t.Fatal(e)
	}
	if mode == "prepare-before" || mode == "prepare-after" {
		stage := "before_commit"
		if mode == "prepare-after" {
			stage = "after_commit"
		}
		s.fault = func(at string) error {
			if at == stage {
				os.Exit(71)
			}
			return nil
		}
		if _, e = s.PrepareCreate(ctx, CreateRequest{"crash-create", "crash fixture", DefaultPolicy(), "deliberate durable boundary test"}); e != nil {
			t.Fatal(e)
		}
		t.Fatal("crash hook not reached")
	}
	op, e := s.PrepareCreate(ctx, CreateRequest{"crash-create", "crash fixture", DefaultPolicy(), "deliberate durable boundary test"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Decide(ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); e != nil {
		t.Fatal(e)
	}
	v, e := s.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	candidate := identityFixture(t, clock.Wall())
	pending, e := s.PrepareAdmission(ctx, rootPrincipal(root), AdmissionRequest{Key: "crash-admit", Candidate: candidate, ConsentHash: hash("fixture locally approved candidate"), HostInbox: true, PolicyRevision: 1, Reason: "deliberate activation boundary"})
	if e != nil {
		t.Fatal(e)
	}
	challenge, proving, e := s.BeginProof(ctx, rootPrincipal(root), pending.Key)
	if e != nil {
		t.Fatal(e)
	}
	stage := "admission_active"
	if mode == "active-after" {
		stage = "after_commit"
	}
	s.fault = func(at string) error {
		if at == stage {
			os.Exit(71)
		}
		return nil
	}
	proof := Proof{Workspace: root.ID, Generation: root.Generation, Source: candidate.Host, Target: self.Host, SourcePin: candidate.Fingerprint, TargetPin: self.Fingerprint, ConsentHash: challenge.ConsentHash, Nonce: challenge.Nonce}
	if _, e = s.CompleteProof(ctx, rootPrincipal(root), pending.Key, proving.Revision, proof); e != nil {
		t.Fatal(e)
	}
	t.Fatal("activation crash hook not reached")
}
func TestActualProcessCrashBeforeAfterSchemaAndAdmissionCommit(t *testing.T) {
	for _, mode := range []string{"prepare-before", "prepare-after", "active-before", "active-after"} {
		t.Run(mode, func(t *testing.T) {
			f := newStoreFixture(t)
			var path string
			if e := f.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); e != nil {
				t.Fatal(e)
			}
			self, _ := json.Marshal(f.self)
			authority, _ := json.Marshal(f.authority)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkspaceCrashBoundaryChild$")
			cmd.Env = append(os.Environ(), "ARM_WORKSPACE_CRASH_MODE="+mode, "ARM_WORKSPACE_DB="+path, "ARM_WORKSPACE_SELF="+string(self), "ARM_WORKSPACE_AUTHORITY="+string(authority), "ARM_WORKSPACE_WALL="+f.clock.Wall().Format(time.RFC3339Nano))
			output, e := cmd.CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != 71 {
				t.Fatalf("child did not hit exact crash boundary: %v %s", e, output)
			}
			restarted, e := Open(f.ctx, f.db, f.authority, f.self, f.clock)
			if e != nil {
				t.Fatal(e)
			}
			v, e := restarted.Read(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "prepare-before":
				if v.Initialized || len(v.Instances) != 0 {
					t.Fatal("partial schema/create survived uncommitted crash", v)
				}
			case "prepare-after":
				if !v.Initialized || len(v.Instances) != 1 || v.Instances[0].State != "prepared" || len(v.Members) != 0 {
					t.Fatal("committed pending review disappeared/activated", v)
				}
				op := v.Operations[0]
				if _, e = restarted.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); code(e) != "review_expired" {
					t.Fatal("dead boot approval revived", e)
				}
			case "active-before", "active-after":
				root := v.Instances[0]
				pending, found := operationOf(&v, root.ID, principalKey(rootPrincipal(root)), "crash-admit")
				if !found {
					t.Fatal("crash erased exact admission")
				}
				if mode == "active-before" {
					if pending.Phase != "proving" || pending.Result != nil {
						t.Fatal("uncommitted activation survived", pending)
					}
					if _, _, e = restarted.BeginProof(f.ctx, rootPrincipal(root), pending.Key); code(e) != "admission_changed" {
						t.Fatal("crash silently replayed proof", e)
					}
				} else if pending.Phase != "completed" || pending.Result == nil || pending.Result.State != "active" {
					t.Fatal("committed activation receipt lost", pending)
				}
			}
		})
	}
}
