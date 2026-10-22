package sshservice

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func statusFixture(r SourceIntent) model.SSHKeyStatus {
	receipt := terminalReceipt(r)
	c, _ := r.ControlCommitment()
	digest, _ := c.Digest()
	raw, _ := json.Marshal(receipt.Operation)
	fence := model.SSHKeyFence{Schema: 1, Source: r.Source, Target: r.Request.Pin.Target, Key: r.Request.Key, State: "admitted", Control: &c, ControlSHA256: digest, OperationSHA256: stateHash(raw), Lease: receipt.Lease.ID, Actor: "peer:" + r.Source, Reason: r.Request.Reason, At: receipt.Operation.AdmittedAt}
	return model.SSHKeyStatus{Schema: 1, Source: r.Source, Target: r.Request.Pin.Target, Key: r.Request.Key, State: "admitted", Fence: &fence, Execution: &receipt}
}
func cloneStatus(s model.SSHKeyStatus) model.SSHKeyStatus {
	raw, _ := json.Marshal(s)
	var out model.SSHKeyStatus
	_ = json.Unmarshal(raw, &out)
	return out
}
func hostileStatuses(valid model.SSHKeyStatus) map[string]model.SSHKeyStatus {
	changes := map[string]func(*model.SSHKeyStatus){
		"unknown-schema":         func(s *model.SSHKeyStatus) { s.Schema = 2 },
		"unknown-state":          func(s *model.SSHKeyStatus) { s.State = "completed" },
		"wrong-source":           func(s *model.SSHKeyStatus) { s.Source = s.Target },
		"wrong-key":              func(s *model.SSHKeyStatus) { s.Key = "other" },
		"absent-with-admission":  func(s *model.SSHKeyStatus) { s.State = "absent" },
		"sealed-with-admission":  func(s *model.SSHKeyStatus) { s.State = "sealed" },
		"missing-fence":          func(s *model.SSHKeyStatus) { s.Fence = nil },
		"missing-operation":      func(s *model.SSHKeyStatus) { s.Execution.Operation = nil },
		"missing-lease":          func(s *model.SSHKeyStatus) { s.Execution.Lease = nil },
		"not-found-with-records": func(s *model.SSHKeyStatus) { s.Execution.Found = false },
		"another-lease":          func(s *model.SSHKeyStatus) { s.Execution.Lease.ID = "other" },
		"another-operation":      func(s *model.SSHKeyStatus) { s.Execution.Operation.Request.Reason = "changed" },
		"operation-hash":         func(s *model.SSHKeyStatus) { s.Fence.OperationSHA256 = s.Fence.ControlSHA256 },
		"control-owner": func(s *model.SSHKeyStatus) {
			s.Fence.Control.Owner = "4294967295"
			s.Fence.ControlSHA256, _ = s.Fence.Control.Digest()
		},
		"fence-actor":              func(s *model.SSHKeyStatus) { s.Fence.Actor = "peer:another" },
		"fence-time":               func(s *model.SSHKeyStatus) { s.Fence.At = s.Fence.At.Add(-1) },
		"queued":                   func(s *model.SSHKeyStatus) { s.Execution.Lease.State = "queued" },
		"unknown-lease-state":      func(s *model.SSHKeyStatus) { s.Execution.Lease.State = "future" },
		"terminal-missing-end":     func(s *model.SSHKeyStatus) { s.Execution.Lease.Ended = nil },
		"terminal-missing-outcome": func(s *model.SSHKeyStatus) { s.Execution.Lease.Outcome = "" },
	}
	out := map[string]model.SSHKeyStatus{}
	for name, change := range changes {
		s := cloneStatus(valid)
		change(&s)
		out[name] = s
	}
	return out
}
func TestKeyStatusExactEnvelopeRelationsAndUnknownStates(t *testing.T) {
	home, b := stateFixture(t)
	p := statePin(t, home, b)
	r := sourceFixture(t, home, b, p, "status-envelope")
	valid := statusFixture(r)
	if e := ValidateKeyStatus(r, valid); e != nil {
		t.Fatal(e)
	}
	for name, s := range hostileStatuses(valid) {
		t.Run(name, func(t *testing.T) {
			if e := ValidateKeyStatus(r, s); e == nil {
				t.Fatal("hostile status accepted", name)
			}
		})
	}
	for _, state := range []string{"active", "provisioning", "ending", "needs_attention", "reclaiming"} {
		s := cloneStatus(valid)
		s.Execution.Lease.State = state
		s.Execution.Lease.Ended = nil
		s.Execution.Lease.Outcome = ""
		if e := ValidateKeyStatus(r, s); e != nil {
			t.Fatal(state, e)
		}
		if e := MatchTerminalReceipt(r, *s.Execution); e == nil {
			t.Fatal("nonterminal status became settlement", state)
		}
	}
	absent := model.SSHKeyStatus{Schema: 1, Source: r.Source, Target: r.Request.Pin.Target, Key: r.Request.Key, State: "absent"}
	if e := ValidateKeyStatus(r, absent); e != nil {
		t.Fatal(e)
	}
	seal := sourceSeal(t, r)
	sealed := absent
	sealed.State = "sealed"
	sealed.Fence = &seal
	if e := ValidateKeyStatus(r, sealed); e != nil {
		t.Fatal(e)
	}
	sealed.Execution = valid.Execution
	if e := ValidateKeyStatus(r, sealed); e == nil {
		t.Fatal("sealed plus execution accepted")
	}
	legacy := cloneStatus(valid)
	legacy.State = "legacy-admitted"
	legacy.Fence = nil
	if e := ValidateKeyStatus(r, legacy); e != nil {
		t.Fatal("pre-fence legacy record refused", e)
	}
	f := *valid.Fence
	f.State = "legacy-admitted"
	f.Control = nil
	f.ControlSHA256 = ""
	f.Actor = "local:" + r.Request.TargetOwner
	legacy.Fence = &f
	if e := ValidateKeyStatus(r, legacy); e != nil {
		t.Fatal("immutable migrated legacy record refused", e)
	}
	legacy.Fence.Control = valid.Fence.Control
	if e := ValidateKeyStatus(r, legacy); e == nil {
		t.Fatal("legacy invented control accepted")
	}
}
func TestKeyStatusDecodeRejectsAliasesNullMissingUnknownAndTrailing(t *testing.T) {
	home, b := stateFixture(t)
	p := statePin(t, home, b)
	r := sourceFixture(t, home, b, p, "status-raw")
	valid := statusFixture(r)
	raw, _ := json.Marshal(valid)
	changes := map[string][]byte{
		"alias":          bytes.Replace(raw, []byte(`"schema":1`), []byte(`"Schema":1`), 1),
		"duplicate":      bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1),
		"null-false":     bytes.Replace(raw, []byte(`"pty":false`), []byte(`"pty":null`), 1),
		"missing":        bytes.Replace(raw, []byte(`"schema":1,`), nil, 1),
		"unknown":        bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"future":false`), 1),
		"nested-unknown": bytes.Replace(raw, []byte(`"resources":{`), []byte(`"resources":{"future":0,`), 1),
		"trailing":       append(append([]byte{}, raw...), []byte(` {}`)...),
	}
	var decoded model.SSHKeyStatus
	if e := DecodeKeyStatus(raw, &decoded); e != nil || !reflect.DeepEqual(decoded, valid) {
		t.Fatal(e)
	}
	for name, bad := range changes {
		t.Run(name, func(t *testing.T) {
			before := cloneStatus(valid)
			out := cloneStatus(valid)
			if e := DecodeKeyStatus(bad, &out); e == nil {
				t.Fatal("noncanonical hostile status decoded")
			}
			if !reflect.DeepEqual(out, before) {
				t.Fatal("failed decoding changed caller's evidence")
			}
		})
	}
}
func TestKeyStatusHostileEnvelopeNeverCleansOrSettlesPublishedArtifacts(t *testing.T) {
	home, b := stateFixture(t)
	statePin(t, home, b)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestArtifactCrashHelper$", "--", home, b.Host, "published")
	out, e := cmd.CombinedOutput()
	if e == nil || cmd.ProcessState.ExitCode() != 21 {
		t.Fatalf("%v %s", e, out)
	}
	state, e := ReadState(t.Context(), home, b)
	if e != nil || len(state.Intents) != 1 {
		t.Fatal(e)
	}
	intent := state.Intents[0]
	valid := statusFixture(intent)
	t.Cleanup(func() {
		for _, name := range append(append([]string{}, artifactNames...), ArtifactReceiptName) {
			os.Remove(filepath.Join(intent.Directory.Path, name))
		}
		os.Remove(intent.Directory.Path)
	})
	manifest, e := os.ReadFile(filepath.Join(intent.Directory.Path, ArtifactReceiptName))
	if e != nil {
		t.Fatal(e)
	}
	for name, bad := range hostileStatuses(valid) {
		if e = RecoverArtifacts(t.Context(), home, b, intent, bad); e == nil {
			t.Fatal("hostile envelope cleaned artifacts", name)
		}
		for _, file := range artifactNames {
			if _, e = os.Lstat(filepath.Join(intent.Directory.Path, file)); e != nil {
				t.Fatal("hostile envelope removed artifact", name, file, e)
			}
		}
		after, e := os.ReadFile(filepath.Join(intent.Directory.Path, ArtifactReceiptName))
		if e != nil || !bytes.Equal(after, manifest) {
			t.Fatal("hostile envelope changed receipt", name, e)
		}
	}
	state, e = ReadState(t.Context(), home, b)
	if e != nil || len(state.Intents) != 1 || len(state.Completions) != 0 || len(state.Seals) != 0 {
		t.Fatal("hostile status settled intention", e)
	}
	if e = RecoverArtifacts(t.Context(), home, b, intent, valid); e != nil {
		t.Fatal("exact valid status could not recover artifacts", e)
	}
	if e = SettleSource(t.Context(), home, b, intent, *valid.Execution); e != nil {
		t.Fatal(e)
	}
}
