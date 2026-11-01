package agentstatus

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func hexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (i Identity) Validate() error {
	c := i.Contact
	if !hexID(i.Host, 32) || c.ID == "" || len(c.ID) > 128 || c.ID == "operator" || c.ID == "broadcast" || !utf8.ValidString(c.ID) || !hexID(c.Registration, 32) {
		return fmt.Errorf("invalid stored status identity")
	}
	id, err := uuid.Parse(c.Session)
	if err != nil || id.String() != c.Session {
		return fmt.Errorf("invalid stored status session")
	}
	if len(c.CWD) > 4096 || !filepath.IsAbs(c.CWD) || len(c.ConfigDir) > 4096 || c.ConfigDir != "" && !filepath.IsAbs(c.ConfigDir) || len(c.Remote) > 4096 || c.Remote != "" && (!strings.HasPrefix(c.Remote, "unix://") || c.Adapter != "codex") {
		return fmt.Errorf("invalid stored status contact paths")
	}
	if c.Adapter != "codex" && c.Adapter != "claude" && c.Adapter != "generic" || c.Delivery != "native" && c.Delivery != "poll" || c.Adapter == "generic" && c.Delivery != "poll" {
		return fmt.Errorf("invalid stored status adapter")
	}
	return nil
}
func (s State) Validate() error {
	bad := func() error { return fmt.Errorf("malformed or inconsistent status state; preserved") }
	if s.Version != Version || s.Entries == nil || s.Attempts == nil || len(s.Entries) > MaxIdentities || len(s.Attempts) > MaxAttempts || s.Config.Validate() != nil || s.Config.Revision < 0 || s.DailyCalls < 0 || s.DailyCalls > 100 {
		return bad()
	}
	if s.Day != "" {
		if _, e := time.Parse("2006-01-02", s.Day); e != nil {
			return bad()
		}
	} else if s.DailyCalls != 0 {
		return bad()
	}
	entries := map[string]Entry{}
	for _, e := range s.Entries {
		if _, exists := entries[e.ID]; exists || e.ID != e.Identity.Key() || e.Identity.Validate() != nil || e.FirstObserved.IsZero() || e.LastObserved.Before(e.FirstObserved) {
			return bad()
		}
		entries[e.ID] = e
	}
	ids, keys := map[string]bool{}, map[string]bool{}
	generations := map[string]int{}
	daily := 0
	states := map[string]bool{}
	for _, v := range strings.Fields("pending_delivery awaiting_reply contributed late_reply ready generating failed cancelled superseded timed_out unknown") {
		states[v] = true
	}
	for _, a := range s.Attempts {
		if ids[a.ID] || keys[a.Key] || !hexID(a.ID, 32) || a.Key == "" || len(a.Key) > 100 || !utf8.ValidString(a.Key) || a.Identity.Validate() != nil || a.EntryID != a.Identity.Key() || entries[a.EntryID].ID == "" || a.Digest != RequestDigest(a.EntryID, a.Identity.Contact) || a.Generation < 1 || a.Generation <= generations[a.EntryID] || !states[a.State] || a.Created.IsZero() || !a.Deadline.Equal(a.Created.Add(2*time.Minute)) || len(a.Error) > 512 {
			return bad()
		}
		ids[a.ID] = true
		keys[a.Key] = true
		generations[a.EntryID] = a.Generation
		if a.Provider != "openai" && a.Provider != "anthropic" || a.Provider == "openai" && a.Model != CodexModel || a.Provider == "anthropic" && a.Model != ClaudeModel {
			return bad()
		}
		if a.MessageID != "" && !hexID(a.MessageID, 32) {
			return bad()
		}
		if a.Account != nil {
			raw, e := json.Marshal(a.Account)
			if e != nil {
				return bad()
			}
			if _, e = DecodeAccount(string(raw)); e != nil || a.Account.RequestID != a.ID || a.ReplyAt == nil || a.ReplyAt.IsZero() || a.MessageID == "" {
				return bad()
			}
			if !hexID(a.ReplyID, 32) && a.ReplyID != a.MessageID+":native" && a.ReplyID != a.MessageID+":ack" {
				return bad()
			}
		} else if a.ReplyAt != nil || a.ReplyID != "" {
			return bad()
		}
		if a.State == "contributed" || a.State == "late_reply" || a.State == "generating" || a.State == "ready" {
			if a.Account == nil {
				return bad()
			}
		}
		if a.CallStarted != nil {
			if a.Account == nil || a.CallStarted.Before(a.Created) {
				return bad()
			}
			if a.CallStarted.UTC().Format("2006-01-02") == s.Day {
				daily++
			}
		} else if a.State == "generating" || a.State == "ready" || a.Usage != nil || a.ReservedBytes != 0 {
			return bad()
		}
		if a.ReservedBytes != 0 && a.ReservedBytes != FinalizationReserve {
			return bad()
		}
		if a.State == "generating" && a.ReservedBytes != FinalizationReserve {
			return bad()
		}
		if a.HTML != "" {
			if a.Generated == nil || a.CallStarted == nil || a.Generated.Before(*a.CallStarted) || a.State != "ready" || a.ReservedBytes != 0 || ValidateDocument(a.HTML) != nil {
				return bad()
			}
		} else if a.Generated != nil || a.State == "ready" {
			return bad()
		}
		if a.Usage != nil {
			u := a.Usage
			if u.Input < 0 || u.Input > MaxPromptBytes*2 || u.Output < 0 || u.Output > OutputTokens || len(u.Billing) > 128 || math.IsNaN(u.EstimatedUSD) || math.IsInf(u.EstimatedUSD, 0) || u.EstimatedUSD < 0 || u.EstimatedUSD > 100 {
				return bad()
			}
		}
	}
	if daily > s.DailyCalls {
		return bad()
	}
	return nil
}
