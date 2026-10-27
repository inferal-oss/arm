package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/usage"
)

func historyCLIState(t *testing.T) (string, sshservice.Binding, sshservice.SourceIntent) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "state")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	native := local.CurrentIdentity()
	b := sshservice.Binding{Host: strings.Repeat("a", 32), Owner: native.UID, Account: native.Username}
	store, e := mesh.Init(home, b.Host, "source-history", "127.0.0.1:19317", "127.0.0.1:19317")
	if e != nil {
		t.Fatal(e)
	}
	config := store.Config()
	store.Close()
	review, e := sshservice.ReviewService(t.Context(), home, b, true, "private source fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.ApplyService(t.Context(), home, b, review); e != nil {
		t.Fatal(e)
	}
	pin := sshservice.Pin{Schema: 1, Source: b.Host, Target: strings.Repeat("b", 32), Pair: strings.Repeat("c", 64), Revision: 3, TLSFingerprint: strings.Repeat("d", 64), TLSSPKI: strings.Repeat("e", 64), Account: "destination", PublicKey: review.After.PublicKey, At: time.Now().UTC()}
	pinReview, e := sshservice.ReviewPin(filepath.Join(home, "mesh"), pin)
	if e != nil {
		t.Fatal(e)
	}
	if e = sshservice.CommitStatePin(t.Context(), home, b, pinReview); e != nil {
		t.Fatal(e)
	}
	directory, e := sshservice.CreateInvocationDirectory(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Remove(directory.Path) })
	worker, e := usage.Capture(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	intent := sshservice.SourceIntent{Schema: 1, Source: b.Host, Owner: b.Owner, Request: sshservice.SourceRequest{Key: "original-source-key", Pin: pin, TargetOwner: "1000", SourceCertificate: config.Identity.Fingerprint, PublicKey: review.After.PublicKey, Reason: "private fixture", TTL: time.Minute, Resources: model.Resources{CPU: 1}, CWD: "/tmp", Kind: "exec", CommandSHA256: strings.Repeat("f", 64)}, Worker: worker, Directory: directory, Created: time.Now().UTC()}
	if e = sshservice.ReserveSource(t.Context(), home, b, intent); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(directory.Path); e != nil {
		t.Fatal(e)
	}
	control, e := intent.ControlCommitment()
	if e != nil {
		t.Fatal(e)
	}
	digest, e := control.Digest()
	if e != nil {
		t.Fatal(e)
	}
	seal := model.SSHKeyFence{Schema: 1, Source: intent.Source, Target: pin.Target, Key: intent.Request.Key, State: "sealed", Control: &control, ControlSHA256: digest, Actor: "peer:" + b.Host, Reason: "explicit fixture nonexecution", At: time.Now().UTC()}
	if e = sshservice.SettleSourceSeal(t.Context(), home, b, intent, seal); e != nil {
		t.Fatal(e)
	}
	return home, b, intent
}
func historyApp(home string, out, stderr *bytes.Buffer) *app {
	return &app{home: home, ctx: context.Background(), in: strings.NewReader(""), out: out, err: stderr}
}
func TestHistoryHelpAndInvalidArgumentsAreOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	for _, topic := range []string{"history", "history ssh-source", "history ssh-source plan", "history ssh-source archive", "history ssh-source inspect", "history ssh-source reconcile", "history ssh-source receipt", "history ssh-source recovery", "history ssh-source recovery plan", "history ssh-source recovery inspect", "history ssh-source recovery apply"} {
		var out, stderr bytes.Buffer
		args := append([]string{"--home", home}, strings.Fields(topic)...)
		if code := Main(append(args, "--help"), strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "arm history") {
			t.Fatal(topic, code, stderr.String())
		}
	}
	for _, args := range [][]string{{"ssh-source", "plan", "extra"}, {"ssh-source", "plan", "--limit", "0"}, {"ssh-source", "plan", "--limit", "2", "--limit", "3"}, {"ssh-source", "plan", "--before", "nonsense"}, {"ssh-source", "archive", "--key", "k", "--yes", "--dry-run"}, {"ssh-source", "archive", "--key", "k", "--expect-plan", "BAD"}, {"ssh-source", "receipt", "node"}, {"ssh-source", "recovery", "inspect"}, {"ssh-source", "recovery", "apply", "--key", "k", "--expect-review", "BAD"}} {
		var out, stderr bytes.Buffer
		a := historyApp(home, &out, &stderr)
		if code, e := a.historyCommand(args); e == nil || code != 2 {
			t.Fatal(args, code, e)
		}
	}
	if _, e := os.Lstat(home); !os.IsNotExist(e) {
		t.Fatal("offline help/invalid inputs initialized state", e)
	}
}
func TestHistoryArchiveConsentDryRunAndExactRetry(t *testing.T) {
	home, b, _ := historyCLIState(t)
	plan, e := sshservice.PlanSourceHistory(t.Context(), home, b, sshservice.SourceHistorySelection{Limit: 64})
	if e != nil {
		t.Fatal(e)
	}
	before := resetCLITree(t, home)
	for _, kind := range []string{"pipe", "dry", "terminal-no", "terminal-empty", "terminal-json"} {
		t.Run(kind, func(t *testing.T) {
			var out, stderr bytes.Buffer
			a := historyApp(home, &out, &stderr)
			args := []string{"ssh-source", "archive", "--key", "archive-one", "--reason", "preserve permanent fences", "--expect-plan", plan.SHA256}
			if kind == "dry" {
				args = append(args, "--dry-run")
			}
			if strings.HasPrefix(kind, "terminal") {
				a.outputTTY, a.inputTTY, a.colors = true, true, true
				a.in = strings.NewReader("n\n")
				if kind == "terminal-empty" {
					a.in = strings.NewReader("\n")
				}
				if kind == "terminal-json" {
					a.forceJSON = true
				}
			}
			code, e := a.historyCommand(args)
			if kind == "dry" {
				if code != 0 || e != nil {
					t.Fatal(code, e)
				}
				var v map[string]any
				if e = json.Unmarshal(out.Bytes(), &v); e != nil || v["dry_run"] != true {
					t.Fatal("not one structured preview", e)
				}
			} else if e == nil {
				t.Fatal("unapproved mutation succeeded")
			}
			if !reflect.DeepEqual(before, resetCLITree(t, home)) {
				t.Fatal("review/decline changed history")
			}
			if strings.HasPrefix(kind, "terminal") && kind != "terminal-json" && !strings.Contains(out.String(), "\x1b[") {
				t.Fatal("terminal review lacked ANSI styling")
			}
		})
	}
	var out, stderr bytes.Buffer
	a := historyApp(home, &out, &stderr)
	code, e := a.historyCommand([]string{"ssh-source", "archive", "--key", "archive-one", "--reason", "preserve permanent fences", "--expect-plan", plan.SHA256, "--yes"})
	if code != 0 || e != nil {
		t.Fatal(code, e)
	}
	var original sshservice.SourceHistoryOperation
	if e = json.Unmarshal(out.Bytes(), &original); e != nil || original.Phase != "completed" {
		t.Fatal("not one final operation", e)
	}
	frozen := resetCLITree(t, home)
	out.Reset()
	code, e = a.historyCommand([]string{"ssh-source", "archive", "--key", "archive-one"})
	if code != 0 || e != nil {
		t.Fatal("retry replanned empty hot selection", code, e)
	}
	var retry sshservice.SourceHistoryOperation
	if e = json.Unmarshal(out.Bytes(), &retry); e != nil || !reflect.DeepEqual(original, retry) {
		t.Fatal("retry changed original result", e)
	}
	for _, changed := range [][]string{{"--reason", "different"}, {"--expect-plan", strings.Repeat("0", 64)}, {"--limit", "1"}, {"--target", strings.Repeat("c", 32)}, {"--before", "2000-01-01T00:00:00Z"}} {
		out.Reset()
		_, e = a.historyCommand(append([]string{"ssh-source", "archive", "--key", "archive-one"}, changed...))
		var detail *model.Error
		if !errors.As(e, &detail) || detail.Code != "idempotency_conflict" {
			t.Fatal(changed, e)
		}
	}
	if !reflect.DeepEqual(frozen, resetCLITree(t, home)) {
		t.Fatal("exact retry/conflicts changed persistent state")
	}
}
func TestHistoryOldKeyPrecedesBackendAndColdLookupSurvivesRemovedPeer(t *testing.T) {
	home, b, intent := historyCLIState(t)
	for _, cold := range []bool{false, true} {
		if cold {
			plan, e := sshservice.PlanSourceHistory(t.Context(), home, b, sshservice.SourceHistorySelection{Limit: 64})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = sshservice.ArchiveSourceHistory(t.Context(), home, b, sshservice.SourceHistoryRequest{Schema: 1, Key: "archive", Reason: "permanent fence", Plan: plan}); e != nil {
				t.Fatal(e)
			}
		}
		before := resetCLITree(t, home)
		var out, stderr bytes.Buffer
		a := historyApp(home, &out, &stderr)
		_, e := a.sshCommand([]string{intent.Request.Pin.Target, "--key", intent.Request.Key, "--reason", "changed request", "--", "exit 99"})
		var detail *model.Error
		if !errors.As(e, &detail) || detail.Code != "ssh_intent_exists" {
			t.Fatal("old key reached unavailable backend", cold, e)
		}
		if code, e := a.sshRecover("lookup", []string{intent.Request.Pin.Target, "--key", intent.Request.Key}); e != nil || code != 0 {
			t.Fatal("offline local lookup required removed peer", e)
		}
		var v struct {
			Source  string                         `json:"source"`
			Receipt sshservice.SourceHistoryLookup `json:"receipt"`
		}
		if e = json.Unmarshal(out.Bytes(), &v); e != nil || v.Source != "local" || !v.Receipt.Found || v.Receipt.Archived != cold {
			t.Fatal("local evidence mislabeled", e)
		}
		if !reflect.DeepEqual(before, resetCLITree(t, home)) {
			t.Fatal("old key lookup/precheck mutated state")
		}
	}
}

func TestHistoryDefiniteCapacityRefusalCleansOnlyBornEmptyDirectory(t *testing.T) {
	home, b, _ := historyCLIState(t)
	for _, kind := range []string{"typed-capacity", "generic-error", "changed-content"} {
		t.Run(kind, func(t *testing.T) {
			directory, e := sshservice.CreateInvocationDirectory(t.Context(), home, b)
			if e != nil {
				t.Fatal(e)
			}
			defer os.Remove(directory.Path)
			refusal := error(sshservice.ErrSourceCapacity)
			if kind == "generic-error" {
				refusal = errors.New("publication acknowledgement unavailable")
			}
			path := filepath.Join(directory.Path, "retain-unreviewed-content")
			if kind == "changed-content" {
				if e = os.WriteFile(path, []byte("retain"), 0600); e != nil {
					t.Fatal(e)
				}
				defer os.Remove(path)
			}
			result, definite := sshReservationRefusal(t.Context(), sshservice.SourceIntent{Directory: directory}, refusal)
			if kind == "typed-capacity" {
				var detail *model.Error
				if !definite || !errors.As(result, &detail) || detail.Code != "capacity_exceeded" {
					t.Fatal(result, definite)
				}
				if _, e = os.Lstat(directory.Path); !os.IsNotExist(e) {
					t.Fatal("definite refusal left born empty directory", e)
				}
			} else {
				if _, e = os.Lstat(directory.Path); e != nil {
					t.Fatal("uncertain/changed directory was removed", e)
				}
				if kind == "generic-error" && definite {
					t.Fatal("generic ambiguity was classified definite")
				}
				if kind == "changed-content" && result == nil {
					t.Fatal("nonempty directory cleanup succeeded")
				}
			}
		})
	}
}

func TestHistoryCapacityChangeAfterPreflightPreservesNoDispatchAndCleansBornDirectory(t *testing.T) {
	home, b, original := historyCLIState(t)
	var out, stderr bytes.Buffer
	a := historyApp(home, &out, &stderr)
	if e := a.sshSourcePrecheck(original.Request.Pin.Target, "raced-new-key"); e != nil {
		t.Fatal(e)
	}
	directory, e := sshservice.CreateInvocationDirectory(t.Context(), home, b)
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(directory.Path)
	next := original
	next.Request.Key = "raced-new-key"
	next.Directory = directory
	next.Created = time.Now().UTC()
	// Other successful source work consumes the promised future completion slots
	// after the read-only preflight but before this source's authoritative reserve.
	for n := 1; n < sshservice.MaxCompletions; n++ {
		item := original
		item.Request.Key = fmt.Sprintf("intervening-%d", n)
		item.Created = time.Now().UTC()
		item.Directory, e = sshservice.CreateInvocationDirectory(t.Context(), home, b)
		if e != nil {
			t.Fatal(e)
		}
		if e = sshservice.ReserveSource(t.Context(), home, b, item); e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(item.Directory.Path); e != nil {
			t.Fatal(e)
		}
		control, e := item.ControlCommitment()
		if e != nil {
			t.Fatal(e)
		}
		digest, e := control.Digest()
		if e != nil {
			t.Fatal(e)
		}
		seal := model.SSHKeyFence{Schema: 1, Source: item.Source, Target: item.Request.Pin.Target, Key: item.Request.Key, State: "sealed", Control: &control, ControlSHA256: digest, Actor: "peer:" + b.Host, Reason: "intervening settled work", At: time.Now().UTC()}
		if e = sshservice.SettleSourceSeal(t.Context(), home, b, item, seal); e != nil {
			t.Fatal(e)
		}
	}
	e = sshservice.ReserveSource(t.Context(), home, b, next)
	if !errors.Is(e, sshservice.ErrSourceCapacity) {
		t.Fatal("raced reserve did not return definite capacity refusal", e)
	}
	result, definite := sshReservationRefusal(t.Context(), next, e)
	var detail *model.Error
	if !definite || !errors.As(result, &detail) || detail.Code != "capacity_exceeded" {
		t.Fatal(result, definite)
	}
	if _, e = os.Lstat(directory.Path); !os.IsNotExist(e) {
		t.Fatal("definite raced refusal retained its empty born directory", e)
	}
	s, e := sshservice.ReadState(t.Context(), home, b)
	if e != nil || len(s.Intents) != 0 || len(s.Seals) != 64 {
		t.Fatal("capacity race created source dispatch evidence or lost settled work", e)
	}
}

func TestHistoryTerminalDryRunUsesCompactReviewWithColorFallback(t *testing.T) {
	home, b, _ := historyCLIState(t)
	before := resetCLITree(t, home)
	plan, e := sshservice.PlanSourceHistory(t.Context(), home, b, sshservice.SourceHistorySelection{Limit: 64})
	if e != nil {
		t.Fatal(e)
	}
	for _, colors := range []bool{true, false} {
		var out, stderr bytes.Buffer
		a := historyApp(home, &out, &stderr)
		a.outputTTY, a.inputTTY, a.colors = true, true, colors
		code, e := a.historyCommand([]string{"ssh-source", "archive", "--key", "compact-dry", "--reason", "Keep immutable receipts", "--expect-plan", plan.SHA256, "--dry-run"})
		if code != 0 || e != nil {
			t.Fatal(code, e)
		}
		text := out.String()
		if !strings.Contains(text, "SSH source history review") || !strings.Contains(text, "Dry run — nothing changed") || strings.Contains(text, "[y/N]") || strings.Contains(text, "roster_sha256") || strings.Contains(text, "intent_sha256") {
			t.Fatal("dry review was not a compact noninteractive window", text)
		}
		if strings.Contains(text, "\x1b[") != colors {
			t.Fatal("color fallback ignored", colors, text)
		}
		if !reflect.DeepEqual(before, resetCLITree(t, home)) {
			t.Fatal("terminal dry run changed history")
		}
	}
}

func TestHistoryAutomaticSSHKeyChecksCorruptClosureBeforeBackend(t *testing.T) {
	home, b, intent := historyCLIState(t)
	plan, e := sshservice.PlanSourceHistory(t.Context(), home, b, sshservice.SourceHistorySelection{Limit: 64})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sshservice.ArchiveSourceHistory(t.Context(), home, b, sshservice.SourceHistoryRequest{Schema: 1, Key: "archive-corruption", Reason: "retain exact tuples", Plan: plan}); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(home, sshservice.SourceHistoryName, sshservice.SourceHistoryCatalogName))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE source_history_refs SET key='retargeted-reference'`); e != nil {
		db.Close()
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	before := resetCLITree(t, home)
	var out, stderr bytes.Buffer
	a := historyApp(home, &out, &stderr)
	_, e = a.sshCommand([]string{intent.Request.Pin.Target, "--reason", "fresh autogenerated key", "--", "exit 0"})
	var detail *model.Error
	if !errors.As(e, &detail) || detail.Code != "ssh_evidence_unavailable" {
		t.Fatal("fresh generated key reached account/network before complete absence closure", e)
	}
	if !reflect.DeepEqual(before, resetCLITree(t, home)) {
		t.Fatal("corrupt preflight changed state")
	}
}
