package sshservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/historypack"
	"github.com/inferal-oss/arm/internal/local"
	"golang.org/x/sys/unix"
)

const (
	SourceHistoryName            = "ssh-source-history"
	SourceHistoryMarkerName      = "ssh-history.json"
	SourceHistoryCatalogName     = "catalog.db"
	SourceHistoryMaxPacks        = 256
	SourceHistoryMaxRefs         = 16384
	SourceHistoryMaxCatalogBytes = 64 << 20
	SourceHistoryPageSize        = 4096
	SourceHistoryMaxPages        = SourceHistoryMaxCatalogBytes / SourceHistoryPageSize
	SourceHistoryMaxPackBytes    = 3 << 20
	SourceHistoryMaxRecordBytes  = 256 << 10
	// Leave room for commit/result/cleanup growth before accepting another op.
	sourceHistoryPageReserve = 512
)

var ErrHistoryBusy = errors.New("SSH source history has an unfinished operation; inspect/reconcile its exact key before new dispatch")
var ErrHistoryUnavailable = errors.New("SSH source history unavailable; preserve exact evidence and do not dispatch")

type SourceHistoryMarker struct {
	Schema     int                  `json:"schema"`
	Binding    Binding              `json:"binding"`
	CatalogID  string               `json:"catalog_id"`
	Directory  historypack.Identity `json:"directory"`
	Catalog    historypack.Identity `json:"catalog"`
	Packs      historypack.Identity `json:"packs"`
	WorkerLock historypack.Identity `json:"worker_lock"`
}
type SourceHistorySummary struct {
	CatalogID string   `json:"catalog_id"`
	Archived  int      `json:"archived_keys"`
	Packs     int      `json:"packs"`
	Busy      []string `json:"unfinished_operations"`
}
type SourceHistorySelection struct {
	Target string    `json:"target,omitempty"`
	Before time.Time `json:"before,omitempty"`
	Limit  int       `json:"limit"`
}
type SourceHistoryMember struct {
	Source          string                `json:"source"`
	Target          string                `json:"target"`
	Key             string                `json:"key"`
	Kind            string                `json:"kind"`
	File            string                `json:"file"`
	Identity        historypack.Identity  `json:"identity"`
	SHA256          string                `json:"sha256"`
	IntentSHA256    string                `json:"intent_sha256"`
	Bytes           int64                 `json:"bytes"`
	PendingFile     string                `json:"pending_file,omitempty"`
	PendingIdentity *historypack.Identity `json:"pending_identity,omitempty"`
	PendingSHA256   string                `json:"pending_sha256,omitempty"`
}
type SourceHistoryPlan struct {
	Schema       int                    `json:"schema"`
	Binding      Binding                `json:"binding"`
	Certificate  string                 `json:"source_certificate"`
	CatalogID    string                 `json:"catalog_id"`
	Selection    SourceHistorySelection `json:"selection"`
	RosterSHA256 string                 `json:"roster_sha256"`
	HotBefore    int                    `json:"hot_before"`
	ColdBefore   int                    `json:"cold_before"`
	Bytes        int64                  `json:"bytes"`
	Members      []SourceHistoryMember  `json:"members"`
	SHA256       string                 `json:"plan_sha256"`
}
type SourceHistoryRequest struct {
	Schema int               `json:"schema"`
	Key    string            `json:"key"`
	Reason string            `json:"reason"`
	Plan   SourceHistoryPlan `json:"plan"`
}
type SourceHistoryOperation struct {
	Schema        int                   `json:"schema"`
	Request       SourceHistoryRequest  `json:"request"`
	RequestSHA256 string                `json:"request_sha256"`
	Actor         local.Identity        `json:"actor"`
	Created       time.Time             `json:"created_at"`
	Updated       time.Time             `json:"updated_at"`
	Revision      int64                 `json:"revision"`
	Phase         string                `json:"phase"`
	PackID        string                `json:"pack_id"`
	Prepared      *historypack.Prepared `json:"prepared,omitempty"`
	Receipt       *historypack.Receipt  `json:"receipt,omitempty"`
	Cleaned       []string              `json:"cleaned"`
	Error         string                `json:"error,omitempty"`
}
type SourceHistoryPack struct {
	Schema    int                 `json:"schema"`
	Operation string              `json:"operation"`
	Members   int                 `json:"members"`
	Receipt   historypack.Receipt `json:"receipt"`
}
type SourceHistoryRef struct {
	Source       string          `json:"source"`
	Target       string          `json:"target"`
	Key          string          `json:"key"`
	Kind         string          `json:"kind"`
	Pack         string          `json:"pack"`
	Record       historypack.Key `json:"record"`
	SHA256       string          `json:"sha256"`
	IntentSHA256 string          `json:"intent_sha256"`
}
type SourceHistoryLookup struct {
	Found      bool                  `json:"found"`
	Archived   bool                  `json:"archived"`
	Kind       string                `json:"kind,omitempty"`
	Completion *SourceCompletion     `json:"completion,omitempty"`
	Seal       *SourceSealCompletion `json:"seal,omitempty"`
}

func historyID(v stateFile) historypack.Identity {
	return historypack.Identity{Device: v.Dev, Inode: v.Ino, Owner: v.UID, Mode: v.Mode}
}
func historyIdentityValid(v historypack.Identity, dir bool) bool {
	mode := uint32(unix.S_IFREG | 0600)
	if dir {
		mode = unix.S_IFDIR | 0700
	}
	return v.Inode != 0 && v.Mode == mode
}
func (m SourceHistoryMarker) Validate() error {
	if m.Schema != 1 || m.Binding.Validate() != nil || !hexString(m.CatalogID, 32) || !historyIdentityValid(m.Directory, true) || !historyIdentityValid(m.Catalog, false) || !historyIdentityValid(m.Packs, true) || !historyIdentityValid(m.WorkerLock, false) {
		return fmt.Errorf("invalid SSH history marker")
	}
	owner := m.Directory.Owner
	if fmt.Sprint(owner) != m.Binding.Owner || m.Catalog.Owner != owner || m.Packs.Owner != owner || m.WorkerLock.Owner != owner {
		return fmt.Errorf("history marker owner differs")
	}
	return nil
}
func sourceHistoryKey(m SourceHistoryMember) historypack.Key {
	domain := "ssh-source-completion-v1"
	if m.Kind == "seal" {
		domain = "ssh-source-seal-v1"
	}
	return historypack.Key{Domain: domain, Principal: m.Source + "/" + m.Target, Value: m.Key}
}
func historyPlanDigest(p SourceHistoryPlan) string {
	p.SHA256 = ""
	raw, e := json.Marshal(p)
	if e != nil {
		return ""
	}
	return stateHash(raw)
}
func (p SourceHistoryPlan) Validate() error {
	if p.Schema != 1 || p.Binding.Validate() != nil || !hexString(p.Certificate, 64) || p.CatalogID != "" && !hexString(p.CatalogID, 32) || !hexString(p.RosterSHA256, 64) || p.Selection.Limit < 1 || p.Selection.Limit > MaxCompletions || p.Selection.Target != "" && !validID(p.Selection.Target) || p.HotBefore < 0 || p.HotBefore > MaxCompletions || p.ColdBefore < 0 || p.ColdBefore > SourceHistoryMaxRefs || p.Bytes < 0 || p.Bytes > MaxCompletions*MaxIntentBytes || len(p.Members) < 1 || len(p.Members) > p.Selection.Limit || !hexString(p.SHA256, 64) || p.SHA256 != historyPlanDigest(p) {
		return fmt.Errorf("invalid source archive plan")
	}
	var n int64
	last := ""
	for _, m := range p.Members {
		tuple := m.Source + "\x00" + m.Target + "\x00" + m.Key
		prefix := "ssh-complete-"
		if m.Kind == "seal" {
			prefix = "ssh-sealed-"
		}
		if m.Source != p.Binding.Host || !validID(m.Target) || !stateText(m.Key, 128) || (m.Kind != "completion" && m.Kind != "seal") || m.File != prefix+stateHash([]byte(tuple))+".json" || tuple <= last || !historyIdentityValid(m.Identity, false) || fmt.Sprint(m.Identity.Owner) != p.Binding.Owner || !hexString(m.SHA256, 64) || !hexString(m.IntentSHA256, 64) || m.Bytes <= 0 || m.Bytes > MaxIntentBytes {
			return fmt.Errorf("invalid exact archive member")
		}
		if (m.PendingFile == "") != (m.PendingIdentity == nil) || (m.PendingFile == "") != (m.PendingSHA256 == "") {
			return fmt.Errorf("partial pending archive evidence")
		}
		if m.PendingFile != "" && (m.PendingFile != "ssh-client-"+stateHash([]byte(tuple))+".json" || !historyIdentityValid(*m.PendingIdentity, false) || fmt.Sprint(m.PendingIdentity.Owner) != p.Binding.Owner || !hexString(m.PendingSHA256, 64) || m.PendingSHA256 != m.IntentSHA256) {
			return fmt.Errorf("invalid pending duplicate archive evidence")
		}
		last = tuple
		n += m.Bytes
	}
	if n != p.Bytes {
		return fmt.Errorf("archive byte count differs")
	}
	return nil
}
func (q SourceHistoryRequest) Validate() error {
	if q.Schema != 1 || !stateText(q.Key, 128) || !stateText(q.Reason, 4096) || q.Plan.Validate() != nil {
		return fmt.Errorf("valid exact archive request/reason required")
	}
	return nil
}
func historyRequestSHA(q SourceHistoryRequest) string {
	raw, e := json.Marshal(q)
	if e != nil {
		return ""
	}
	return stateHash(raw)
}
func decodeHistory(raw []byte, out any) error {
	if len(raw) > SourceHistoryMaxRecordBytes {
		return fmt.Errorf("source archive record exceeds bound")
	}
	if e := preflightSourceHistory(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	encoded, e := json.Marshal(out)
	if e != nil || !bytes.Equal(raw, encoded) {
		return fmt.Errorf("noncanonical/alias/null/unknown history evidence")
	}
	return nil
}
func sortHistoryMembers(v []SourceHistoryMember) {
	sort.Slice(v, func(i, j int) bool {
		a, b := v[i], v[j]
		return strings.Compare(a.Source+"\x00"+a.Target+"\x00"+a.Key, b.Source+"\x00"+b.Target+"\x00"+b.Key) < 0
	})
}
