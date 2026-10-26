package sshservice

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/inferal-oss/arm/internal/historypack"
	"github.com/inferal-oss/arm/internal/local"
)

const SourceHistoryRecoveryName = "ssh-history-recovery.json"
const SourceHistoryRecoveryMaxBytes = 64 << 10
const SourceHistoryRecoveryMaxAttempts = 64

type SourceHistoryRecoveryAttempt struct {
	Key           string                       `json:"key"`
	Reason        string                       `json:"reason"`
	Actor         local.Identity               `json:"actor"`
	Journal       historypack.Identity         `json:"journal"`
	JournalBytes  int64                        `json:"journal_bytes"`
	JournalSHA256 string                       `json:"journal_sha256"`
	Started       time.Time                    `json:"started_at"`
	Completed     *time.Time                   `json:"completed_at,omitempty"`
	Result        *SourceHistoryRecoveryResult `json:"result,omitempty"`
}
type SourceHistoryRecovery struct {
	Schema   int                            `json:"schema"`
	Marker   SourceHistoryMarker            `json:"marker"`
	Attempts []SourceHistoryRecoveryAttempt `json:"attempts"`
}

func (v SourceHistoryRecovery) Validate() error {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > SourceHistoryRecoveryMaxBytes || v.Schema != 1 || v.Marker.Validate() != nil || v.Attempts == nil || len(v.Attempts) < 1 || len(v.Attempts) > SourceHistoryRecoveryMaxAttempts {
		return fmt.Errorf("source recovery scope/count/bytes invalid")
	}
	seen := map[string]bool{}
	var last time.Time
	for i, a := range v.Attempts {
		if !stateText(a.Key, 128) || seen[a.Key] || !stateText(a.Reason, 4096) || a.Actor.UID != v.Marker.Binding.Owner || a.Actor.Username != v.Marker.Binding.Account || !historyIdentityValid(a.Journal, false) || fmt.Sprint(a.Journal.Owner) != v.Marker.Binding.Owner || a.JournalBytes <= 0 || a.JournalBytes > SourceHistoryMaxCatalogBytes+4096 || !hexString(a.JournalSHA256, 64) || a.Started.IsZero() || a.Started.Before(last) || a.Completed == nil && i != len(v.Attempts)-1 || a.Completed != nil && a.Completed.Before(a.Started) || (a.Completed == nil) != (a.Result == nil) || a.Result != nil && a.Result.Validate() != nil {
			return fmt.Errorf("invalid exact recovery attempt")
		}
		seen[a.Key] = true
		last = a.Started
	}
	return nil
}
func decodeSourceRecovery(raw []byte, out *SourceHistoryRecovery) error {
	if len(raw) > SourceHistoryRecoveryMaxBytes {
		return fmt.Errorf("source recovery file exceeds64KiB")
	}
	if e := decodeHistory(raw, out); e != nil {
		return e
	}
	return out.Validate()
}
func (v SourceHistoryRecovery) Pending() bool {
	return len(v.Attempts) > 0 && v.Attempts[len(v.Attempts)-1].Completed == nil
}

type SourceHistoryRecoveryResult struct {
	CatalogSHA256 string `json:"catalog_sha256"`
	CatalogBytes  int64  `json:"catalog_bytes"`
	Packs         int    `json:"packs"`
	Archived      int    `json:"archived_keys"`
}

func (r SourceHistoryRecoveryResult) Validate() error {
	if !hexString(r.CatalogSHA256, 64) || r.CatalogBytes <= 0 || r.CatalogBytes > SourceHistoryMaxCatalogBytes || r.Packs < 0 || r.Packs > SourceHistoryMaxPacks || r.Archived < 0 || r.Archived > SourceHistoryMaxRefs {
		return fmt.Errorf("invalid recovered catalog evidence")
	}
	return nil
}

type SourceHistoryRecoveryRequest struct {
	Schema        int                  `json:"schema"`
	Key           string               `json:"key"`
	Reason        string               `json:"reason"`
	Marker        SourceHistoryMarker  `json:"marker"`
	Journal       historypack.Identity `json:"journal"`
	JournalBytes  int64                `json:"journal_bytes"`
	JournalSHA256 string               `json:"journal_sha256"`
}

func (q SourceHistoryRecoveryRequest) Validate() error {
	if q.Schema != 1 || !stateText(q.Key, 128) || !stateText(q.Reason, 4096) || q.Marker.Validate() != nil || !historyIdentityValid(q.Journal, false) || q.Journal.Owner != q.Marker.Catalog.Owner || q.JournalBytes <= 0 || q.JournalBytes > SourceHistoryMaxCatalogBytes+4096 || !hexString(q.JournalSHA256, 64) {
		return fmt.Errorf("invalid exact catalog recovery request")
	}
	return nil
}
func recoveryRequest(marker SourceHistoryMarker, a SourceHistoryRecoveryAttempt) SourceHistoryRecoveryRequest {
	return SourceHistoryRecoveryRequest{1, a.Key, a.Reason, marker, a.Journal, a.JournalBytes, a.JournalSHA256}
}
