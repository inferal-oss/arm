package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
)

func TestSSHSealedKeyProofAndClosedControl(t *testing.T) {
	for _, change := range []string{"none", "missing-fence", "alias-control", "null-control", "missing-marker"} {
		t.Run(change, func(t *testing.T) {
			home := proofFixture(t, false)
			m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			status, e := m.Status()
			if e != nil {
				t.Fatal(e)
			}
			c := model.SSHControlCommitment{Schema: 1, Source: strings.Repeat("a", 32), Target: status.Host, Pair: strings.Repeat("b", 64), Revision: 1, Certificate: strings.Repeat("c", 64), Owner: status.Owner, Account: status.OwnerUsername, HostKeySHA256: strings.Repeat("d", 64), Key: "no-execution", PublicKeySHA256: strings.Repeat("e", 64), Reason: "private nonexecution proof", TTL: time.Minute, Resources: model.Resources{CPU: 1}, CWD: "/tmp", Kind: "exec", CommandSHA256: strings.Repeat("f", 64)}
			got, e := m.SealSSHKeyAsOperator(context.Background(), c, "prevent late execution")
			if e != nil {
				t.Fatal(e)
			}
			if got.State != "sealed" {
				t.Fatal(got)
			}
			m.Close()
			db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			switch change {
			case "missing-fence":
				_, e = db.Exec(`DROP TABLE ssh_key_fences`)
			case "missing-marker":
				_, e = db.Exec(`DELETE FROM metadata WHERE key='ssh_fence_schema'`)
			case "alias-control":
				_, e = db.Exec(`UPDATE ssh_key_fences SET data=replace(data,'"pty":false','"PTY":false')`)
			case "null-control":
				_, e = db.Exec(`UPDATE ssh_key_fences SET data=json_set(data,'$.control',NULL)`)
			}
			if e != nil {
				t.Fatal(e)
			}
			db.Close()
			p, e := prove(t, home)
			if change == "none" {
				if e != nil || !p.Settled {
					t.Fatalf("valid sealed key: %+v %v", p, e)
				}
			} else if e == nil {
				t.Fatal("corrupt fence accepted", change)
			}
		})
	}
}

func TestSSHNonexecutionSourceProofAndContradictoryHistory(t *testing.T) {
	for _, change := range []string{"none", "alias", "contradiction", "pending"} {
		t.Run(change, func(t *testing.T) {
			home, b, pin := sshProofState(t)
			intent := sshProofIntent(t, home, b, pin)
			c, e := intent.ControlCommitment()
			if e != nil {
				t.Fatal(e)
			}
			digest, e := c.Digest()
			if e != nil {
				t.Fatal(e)
			}
			fence := model.SSHKeyFence{Schema: 1, Source: c.Source, Target: c.Target, Key: c.Key, State: "sealed", Control: &c, ControlSHA256: digest, Actor: "peer:" + c.Source, Reason: "seal exact delayed key", At: time.Now().Add(-time.Minute).UTC()}
			if e = os.Remove(intent.Directory.Path); e != nil {
				t.Fatal(e)
			}
			if e = sshservice.SettleSourceSeal(context.Background(), home, b, intent, fence); e != nil {
				t.Fatal(e)
			}
			base := "ssh-sealed-" + proofSSHHash([]byte(intent.Source+"\x00"+pin.Target+"\x00"+intent.Request.Key)) + ".json"
			path := filepath.Join(home, "mesh", base)
			switch change {
			case "alias":
				raw, _ := os.ReadFile(path)
				raw = []byte(strings.Replace(string(raw), `"pty":false`, `"PTY":false`, 1))
				if e = os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "contradiction":
				complete := sshservice.SourceCompletion{Schema: 1, Intent: intent, Receipt: proofSSHReceipt(intent), At: time.Now().UTC()}
				raw, _ := json.Marshal(complete)
				if e = os.WriteFile(filepath.Join(home, "mesh", strings.Replace(base, "ssh-sealed-", "ssh-complete-", 1)), raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "pending":
				raw, _ := json.Marshal(intent)
				if e = os.WriteFile(filepath.Join(home, "mesh", strings.Replace(base, "ssh-sealed-", "ssh-client-", 1)), raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			r, e := review(t, home)
			if change == "alias" || change == "contradiction" {
				if e == nil {
					r.Close()
					t.Fatal("invalid sealed source history accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			if r.Proof.Settled != (change != "pending") {
				t.Fatal("pending evidence lost or exact sealed evidence refused", r.Proof)
			}
		})
	}
}
