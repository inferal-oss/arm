// Package snapshotlease describes private no-work S reservations. It grants no
// native work, allocation, publication, consumer, cleanup or process authority.
package snapshotlease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
)

const (
	MaxCopies        = 16
	MaxRecordBytes   = 16 << 10
	MaxRequestBytes  = 1 << 20
	PartBytes        = 8 << 10
	MaxParts         = MaxRequestBytes / PartBytes
	MaxRequestTotal  = 8 << 20
	MaxJournalRows   = 256
	MaxJournalBytes  = 2 << 20
	MaxJournalRecord = 8 << 10
	WorkKind         = "share-snapshot-work"
	IdlePolicy       = "idle-native-v1"
)

type Request struct {
	Schema         int                     `json:"schema"`
	Key            string                  `json:"key"`
	Source         string                  `json:"source_storage"`
	SourceRevision int                     `json:"source_revision"`
	Selections     []model.ExportSelection `json:"selections"`
	Policy         string                  `json:"policy"`
	Reason         string                  `json:"reason"`
	WorkTTL        time.Duration           `json:"work_ttl_ns"`
	Retention      time.Duration           `json:"retention_ns"`
	ContentCeiling int64                   `json:"content_ceiling"`
	Resources      model.Resources         `json:"work_resources"`
	Budget         string                  `json:"budget"`
}

func ID(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == v
}
func Digest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func text(v string, n int) bool {
	return len(v) > 0 && len(v) <= n && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}
func (q Request) Validate() error {
	if q.Schema != 1 || !text(q.Key, 128) || !ID(q.Source) || q.SourceRevision < 1 || q.Policy != IdlePolicy || (!text(q.Reason, 4096) || strings.TrimSpace(q.Reason) == "") || q.WorkTTL <= 0 || q.WorkTTL > 7*24*time.Hour || q.Retention <= 0 || q.Retention > 7*24*time.Hour || q.ContentCeiling < 0 || q.ContentCeiling > storage.MaxExportBytes || q.Resources.CPU < 1 || math.IsNaN(q.Resources.CPU) || math.IsInf(q.Resources.CPU, 0) || q.Resources.Memory < storage.SnapshotCopyMemoryFloor || q.Resources.Disk != 0 || (q.Budget != "" && !text(q.Budget, 128)) {
		return fmt.Errorf("closed bounded idle-policy snapshot reservation required")
	}
	normal, e := storage.NormalizeExportSelections(q.Selections)
	if e != nil || !reflect.DeepEqual(normal, q.Selections) {
		return fmt.Errorf("exact canonical selections required")
	}
	return nil
}

// Record contains no selected paths or executable/native handles. The complete
// immutable request is separately bounded, hashed and split into exact parts.
type Record struct {
	Schema              int                `json:"schema"`
	ID                  string             `json:"id"`
	Authority           string             `json:"authority"`
	Owner               string             `json:"owner"`
	Runtime             string             `json:"runtime"`
	Key                 string             `json:"key"`
	RequestSHA256       string             `json:"request_sha256"`
	RequestBytes        int                `json:"request_bytes"`
	RequestParts        int                `json:"request_parts"`
	Source              string             `json:"source_storage"`
	SourceRevision      int                `json:"source_revision"`
	SourceIdentity      model.FileIdentity `json:"source_identity"`
	ProviderIdentity    model.FileIdentity `json:"provider_identity"`
	WorkLease           string             `json:"work_lease"`
	DestinationLease    string             `json:"destination_lease"`
	Created             time.Time          `json:"created"`
	Updated             time.Time          `json:"updated"`
	WorkExpires         time.Time          `json:"work_expires"`
	DestinationExpires  time.Time          `json:"destination_expires"`
	Revision            int64              `json:"revision"`
	Phase               string             `json:"phase"`
	PlanningAttempted   bool               `json:"planning_attempted"`
	AllocationAttempted bool               `json:"allocation_attempted"`
	FinalSlots          int                `json:"final_slots"`
	Decision            string             `json:"decision"`
}

func NativeIdentity(v model.FileIdentity, owner string) bool {
	u, e := strconv.ParseUint(owner, 10, 32)
	return e == nil && strconv.FormatUint(u, 10) == owner && v.Owner == uint32(u) && v.Inode != 0 && v.BirthKnown && v.BornSeconds > 0 && v.BornNanoseconds >= 0 && v.BornNanoseconds < 1e9
}
func decisionText(v string) bool {
	raw, e := json.Marshal(v)
	return e == nil && text(v, 4096) && len(raw) <= 4096
}
func (r Record) Validate() error {
	if r.Schema != 1 || !ID(r.ID) || !ID(r.Authority) || !ID(r.Runtime) || !text(r.Key, 128) || !Digest(r.RequestSHA256) || r.RequestBytes < 1 || r.RequestBytes > MaxRequestBytes || r.RequestParts != (r.RequestBytes+PartBytes-1)/PartBytes || !ID(r.Source) || r.SourceRevision < 1 || !NativeIdentity(r.SourceIdentity, r.Owner) || !NativeIdentity(r.ProviderIdentity, r.Owner) || !ID(r.WorkLease) || !ID(r.DestinationLease) || r.WorkLease == r.DestinationLease || r.Source == r.DestinationLease || r.Source == r.WorkLease || r.Revision < 1 || r.Created.IsZero() || r.Updated.Before(r.Created) || !r.WorkExpires.After(r.Created) || !r.DestinationExpires.After(r.Created) || r.PlanningAttempted || r.AllocationAttempted {
		return fmt.Errorf("invalid no-work snapshot record")
	}
	if r.Phase == "reserved-no-work" && r.Revision == 1 && r.FinalSlots == 1 && r.Decision == "" && r.Created.Equal(r.Updated) {
		return nil
	}
	if r.Phase == "cancelled-no-work" && r.Revision == 2 && r.FinalSlots == 0 && decisionText(r.Decision) {
		return nil
	}
	return fmt.Errorf("unknown or impossible no-work transition")
}

func Canonical(v any, limit int) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) == 0 || len(raw) > limit {
		return nil, fmt.Errorf("canonical evidence byte bound exceeded")
	}
	return raw, nil
}
func Hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// Exact byte comparison additionally refuses duplicate/aliased/missing/null
// fields, JSON whitespace and alternate encodings in durable canonical evidence.
func Decode[T any](raw []byte, limit int) (out T, err error) {
	if len(raw) == 0 || len(raw) > limit {
		return out, fmt.Errorf("bounded canonical evidence required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil {
		return out, err
	}
	b, e := Canonical(out, limit)
	if e != nil || !bytes.Equal(raw, b) {
		return out, fmt.Errorf("noncanonical or incomplete evidence")
	}
	return out, nil
}

type Journal struct {
	Schema     int    `json:"schema"`
	Copy       string `json:"copy"`
	Revision   int64  `json:"revision"`
	Transition string `json:"transition"`
	Before     string `json:"before"`
	After      string `json:"after"`
}
