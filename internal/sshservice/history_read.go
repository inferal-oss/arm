package sshservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/historypack"
)

func sourceHistoryLookupPayload(ctx context.Context, c *historyCatalog, target, key string) (SourceHistoryLookup, []byte, error) {
	ref, e := c.ref(ctx, target, key)
	if errors.Is(e, sql.ErrNoRows) {
		if _, e = verifySourceCatalogAbsence(ctx, c, target, key); e != nil {
			return SourceHistoryLookup{}, nil, e
		}
		return SourceHistoryLookup{}, nil, nil
	}
	if e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	row, e := c.pack(ctx, ref.Pack)
	if e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	op, e := c.lookupOp(ctx, row.Operation)
	if e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	if (op.Phase != "committed" && op.Phase != "completed") || op.Receipt == nil || *op.Receipt != row.Receipt || row.Members != len(op.Request.Plan.Members) {
		return SourceHistoryLookup{}, nil, fmt.Errorf("cold source pack lacks committed exact operation")
	}
	found := false
	for _, m := range op.Request.Plan.Members {
		if m.Source == ref.Source && m.Target == ref.Target && m.Key == ref.Key && m.Kind == ref.Kind && m.SHA256 == ref.SHA256 && m.IntentSHA256 == ref.IntentSHA256 {
			found = true
		}
	}
	if !found {
		return SourceHistoryLookup{}, nil, fmt.Errorf("cold source key lost original reviewed membership")
	}
	pack, e := historypack.Open(ctx, filepath.Join(c.path, "packs"), row.Receipt)
	if e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	defer pack.Close()
	manifest, e := pack.Manifest()
	if e != nil || len(manifest.Entries) != row.Members {
		return SourceHistoryLookup{}, nil, fmt.Errorf("cold source pack membership count differs")
	}
	raw, e := pack.Read(ctx, ref.Record)
	if e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	if len(raw) > MaxIntentBytes || stateHash(raw) != ref.SHA256 {
		return SourceHistoryLookup{}, nil, fmt.Errorf("cold source payload bound/hash differs")
	}
	result := SourceHistoryLookup{Found: true, Archived: true, Kind: ref.Kind}
	var intent SourceIntent
	if ref.Kind == "completion" {
		var v SourceCompletion
		if e = DecodeStateRecord(raw, &v); e != nil || v.Validate() != nil {
			return SourceHistoryLookup{}, nil, fmt.Errorf("cold completion is not exact settled producer evidence")
		}
		result.Completion = &v
		intent = v.Intent
	} else {
		var v SourceSealCompletion
		if e = DecodeStateRecord(raw, &v); e != nil || v.Validate() != nil {
			return SourceHistoryLookup{}, nil, fmt.Errorf("cold seal is not exact nonexecution producer evidence")
		}
		result.Seal = &v
		intent = v.Intent
	}
	original, _ := json.Marshal(intent)
	if intent.Source != ref.Source || intent.Owner != c.marker.Binding.Owner || intent.Request.Pin.Target != target || intent.Request.Key != key || stateHash(original) != ref.IntentSHA256 {
		return SourceHistoryLookup{}, nil, fmt.Errorf("cold payload original intention differs")
	}
	if e = c.guard(); e != nil {
		return SourceHistoryLookup{}, nil, e
	}
	return result, raw, nil
}

// LookupSourceHistory is local immutable evidence inspection. It neither
// resolves a current peer nor performs a network request or grants execution.
func LookupSourceHistory(ctx context.Context, home string, b Binding, target, key string) (SourceHistoryLookup, error) {
	if !validID(target) || !stateText(key, 128) {
		return SourceHistoryLookup{}, fmt.Errorf("exact stable target and source key required")
	}
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return SourceHistoryLookup{}, e
	}
	return lookupSourceState(ctx, s, target, key)
}
func lookupSourceState(ctx context.Context, s *State, target, key string) (SourceHistoryLookup, error) {
	hot := SourceHistoryLookup{}
	var hotRaw []byte
	for _, v := range s.Completions {
		if v.Intent.Request.Pin.Target == target && v.Intent.Request.Key == key {
			copy := v
			hot = SourceHistoryLookup{Found: true, Kind: "completion", Completion: &copy}
			hotRaw = s.contents[completionName(v.Intent)]
		}
	}
	for _, v := range s.Seals {
		if v.Intent.Request.Pin.Target == target && v.Intent.Request.Key == key {
			if hot.Found {
				return SourceHistoryLookup{}, fmt.Errorf("source key has conflicting hot settlement")
			}
			copy := v
			hot = SourceHistoryLookup{Found: true, Kind: "seal", Seal: &copy}
			hotRaw = s.contents[sealCompletionName(v.Intent)]
		}
	}
	if s.historyMarker == nil {
		return hot, nil
	}
	c, e := openHistoryCatalog(ctx, s.home, s.Binding, *s.historyMarker, false, false, nil)
	if e != nil {
		return SourceHistoryLookup{}, e
	}
	defer c.close()
	cold, raw, e := sourceHistoryLookupPayload(ctx, c, target, key)
	if e != nil {
		return SourceHistoryLookup{}, e
	}
	if cold.Found && hot.Found && (cold.Kind != hot.Kind || string(hotRaw) != string(raw)) {
		return SourceHistoryLookup{}, fmt.Errorf("hot/cold source receipts disagree")
	}
	if cold.Found {
		if e = checkSourceHistoryOverlap(s, cold, raw); e != nil {
			return SourceHistoryLookup{}, e
		}
		return cold, nil
	}
	return hot, nil
}

// PlanSourceHistory performs no initialization or writes. The exact selected
// family settlement and complete hot roster digest are rechecked under owner
// locks after approval; a read-only plan never reserves capacity.
func PlanSourceHistory(ctx context.Context, home string, b Binding, selection SourceHistorySelection) (SourceHistoryPlan, error) {
	s, e := ReadState(ctx, home, b)
	if e != nil {
		return SourceHistoryPlan{}, e
	}
	return planSourceState(ctx, s, selection)
}
func planSourceState(ctx context.Context, s *State, selection SourceHistorySelection) (SourceHistoryPlan, error) {
	p := SourceHistoryPlan{Schema: 1, Binding: s.Binding, Certificate: s.config.Identity.Fingerprint, Selection: selection, HotBefore: len(s.Completions) + len(s.Seals), Members: []SourceHistoryMember{}}
	if selection.Limit < 1 || selection.Limit > MaxCompletions || selection.Target != "" && !validID(selection.Target) {
		return p, fmt.Errorf("valid target and1..64 source selection limit required")
	}
	if s.History != nil {
		p.ColdBefore = s.History.Archived
		p.CatalogID = s.History.CatalogID
		if len(s.History.Busy) > 0 {
			return p, ErrHistoryBusy
		}
	}
	roster := []string{}
	for name, raw := range s.contents {
		if stringsSourceRecord(name) {
			roster = append(roster, name+":"+stateHash(raw))
		}
	}
	sort.Strings(roster)
	raw, _ := json.Marshal(roster)
	p.RosterSHA256 = stateHash(raw)
	appendMember := func(intent SourceIntent, kind, name string, at time.Time) error {
		if selection.Target != "" && intent.Request.Pin.Target != selection.Target || !selection.Before.IsZero() && !at.Before(selection.Before) {
			return nil
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e := os.Lstat(intent.Directory.Path); !os.IsNotExist(e) {
			return fmt.Errorf("source invocation artifacts remain or are unavailable; nothing archived")
		}
		original, _ := json.Marshal(intent)
		file := s.files[name]
		m := SourceHistoryMember{Source: intent.Source, Target: intent.Request.Pin.Target, Key: intent.Request.Key, Kind: kind, File: name, Identity: historyID(file), SHA256: file.SHA, IntentSHA256: stateHash(original), Bytes: file.Size}
		if pending, ok := s.files[intentName(intent)]; ok {
			id := historyID(pending)
			m.PendingFile = intentName(intent)
			m.PendingIdentity = &id
			m.PendingSHA256 = pending.SHA
		}
		p.Members = append(p.Members, m)
		return nil
	}
	for _, v := range s.Completions {
		if e := v.Validate(); e != nil {
			return p, e
		}
		if e := appendMember(v.Intent, "completion", completionName(v.Intent), v.At); e != nil {
			return p, e
		}
	}
	for _, v := range s.Seals {
		if e := v.Validate(); e != nil {
			return p, e
		}
		if e := appendMember(v.Intent, "seal", sealCompletionName(v.Intent), v.At); e != nil {
			return p, e
		}
	}
	sortHistoryMembers(p.Members)
	if len(p.Members) > selection.Limit {
		p.Members = p.Members[:selection.Limit]
	}
	for _, m := range p.Members {
		p.Bytes += m.Bytes
	}
	p.SHA256 = historyPlanDigest(p)
	if len(p.Members) == 0 {
		return p, fmt.Errorf("no existing verified settled source receipts match; pending/unknown work is never inferred settled")
	}
	return p, p.Validate()
}
func stringsSourceRecord(name string) bool {
	return strings.HasPrefix(name, "ssh-complete-") || strings.HasPrefix(name, "ssh-sealed-") || strings.HasPrefix(name, "ssh-client-")
}
