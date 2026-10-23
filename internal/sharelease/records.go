// Package sharelease defines closed durable read-only share evidence. It grants
// no path adoption, network identity, execution, mount or cleanup authority.
package sharelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
)

const (
	MaxPublications       = 16
	MaxConsumers          = 4096
	MaxManifestRawBytes   = 16 << 20
	MaxJournalRows        = 16384
	MaxJournalBytes       = 16 << 20
	MaxRecordBytes        = 16 << 10
	MaxJournalRecordBytes = 8 << 10
	PublicationFinalSlots = 8
	ConsumerFinalSlots    = 4
)

// SchemaStatements returns a fresh optional-group catalog; reads never initialize it.
func SchemaStatements() []string {
	return []string{
		`CREATE TABLE share_publications (id TEXT PRIMARY KEY,data TEXT NOT NULL)`,
		`CREATE TABLE share_manifest_parts (share TEXT NOT NULL,generation TEXT NOT NULL,part INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(share,generation,part))`,
		`CREATE TABLE share_consumers (principal TEXT NOT NULL,key TEXT NOT NULL,lease TEXT NOT NULL UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`,
		`CREATE TABLE share_key_fences (principal TEXT NOT NULL,key TEXT NOT NULL,control_sha256 TEXT NOT NULL,state TEXT NOT NULL,lease TEXT UNIQUE,data TEXT NOT NULL,PRIMARY KEY(principal,key))`,
		`CREATE TABLE share_journal (sequence INTEGER PRIMARY KEY AUTOINCREMENT,data TEXT NOT NULL)`,
	}
}

// Worker is a retained native fingerprint, NEVER permission to signal a process.
// PID/PGID are descriptive only; restarted code cannot recreate a live I/O handle.
type Worker struct {
	PID       int    `json:"pid"`
	PGID      int    `json:"pgid"`
	UID       string `json:"uid"`
	Start     string `json:"start"`
	Namespace string `json:"namespace"`
}

func (w Worker) Validate(owner string) error {
	if w.PID < 1 || int64(w.PID) > math.MaxInt32 || w.PGID < 0 || int64(w.PGID) > math.MaxInt32 || w.UID != owner || !uid(owner) || !text(w.Start, 256) || !text(w.Namespace, 256) {
		return fmt.Errorf("exact native worker fingerprint required")
	}
	return nil
}

type PrepareRequest struct {
	MemoryPolicy    string          `json:"memory_policy"`
	Key             string          `json:"key"`
	Storage         string          `json:"storage"`
	StorageRevision int             `json:"storage_revision"`
	Reason          string          `json:"reason"`
	TTL             time.Duration   `json:"ttl_ns"`
	Resources       model.Resources `json:"resources"`
	Budget          string          `json:"budget"`
}

func (q PrepareRequest) Validate() error {
	if q.MemoryPolicy != PreparationMemory().Policy || !key(q.Key) || !id(q.Storage) || q.StorageRevision < 1 || !text(q.Reason, 4096) || q.TTL <= 0 || q.TTL > 7*24*time.Hour || !resources(q.Resources) || q.Resources.CPU < 1 || q.Resources.Memory < PreparationMemory().Total || q.Resources.Disk != 0 || q.Budget != "" && !key(q.Budget) {
		return fmt.Errorf("bounded charged preparation, exact storage and required reason/TTL needed")
	}
	return nil
}

// PublishRequest is one immutable decision per generation. Exact retries only
// inspect its recorded phase; they never enter native verification again.
type PublishRequest struct {
	Schema         int    `json:"schema"`
	Key            string `json:"key"`
	Share          string `json:"share"`
	Generation     string `json:"generation"`
	Revision       int64  `json:"revision"`
	PreparedSHA256 string `json:"prepared_sha256"`
	Reason         string `json:"reason"`
}

func (q PublishRequest) Validate() error {
	if q.Schema != 1 || !key(q.Key) || !id(q.Share) || !id(q.Generation) || q.Revision < 1 || !digest(q.PreparedSHA256) || !text(q.Reason, 4096) {
		return fmt.Errorf("exact keyed publication approval required")
	}
	return nil
}

type Publication struct {
	Schema            int                `json:"schema"`
	Authority         string             `json:"authority"`
	Owner             string             `json:"owner"`
	ID                string             `json:"id"`
	Generation        string             `json:"generation"`
	Request           PrepareRequest     `json:"request"`
	Lease             string             `json:"lease"`
	StorageIdentity   model.FileIdentity `json:"storage_identity"`
	PreparationWorker Worker             `json:"preparation_worker"`
	WorkerPhase       string             `json:"worker_phase"`
	Publish           *PublishRequest    `json:"publish_request,omitempty"`
	Worker            Worker             `json:"worker"`
	WorkerState       string             `json:"worker_state"`
	State             string             `json:"state"`
	Revision          int64              `json:"revision"`
	Created           time.Time          `json:"created_at"`
	Updated           time.Time          `json:"updated_at"`
	Expires           time.Time          `json:"expires_at"`
	Summary           *ManifestSummary   `json:"summary,omitempty"`
	Prepared          *time.Time         `json:"prepared_at,omitempty"`
	Published         *time.Time         `json:"published_at,omitempty"`
	Retired           *time.Time         `json:"retired_at,omitempty"`
	Reason            string             `json:"transition_reason"`
	Failure           string             `json:"failure"`
	JournalSlots      int                `json:"journal_slots"`
}
type ManifestSummary struct {
	SHA256       string `json:"sha256"`
	RawBytes     int    `json:"raw_bytes"`
	StoredBytes  int    `json:"stored_bytes"`
	Parts        int    `json:"parts"`
	Entries      int    `json:"entries"`
	Chunks       int    `json:"chunks"`
	ContentBytes int64  `json:"content_bytes"`
}

func (p Publication) Validate() error {
	if p.Schema != 2 || !id(p.Authority) || !uid(p.Owner) || !id(p.ID) || !id(p.Generation) || !id(p.Lease) || p.Revision < 1 || p.Request.Validate() != nil || p.Worker.Validate(p.Owner) != nil || p.PreparationWorker.Validate(p.Owner) != nil || !stamp(p.Created) || !stamp(p.Updated) || !stamp(p.Expires) || p.Updated.Before(p.Created) || !p.Expires.After(p.Created) || p.Expires.After(p.Created.Add(p.Request.TTL)) || !text(p.Reason, 4096) || (p.Failure != "" && !text(p.Failure, 4096)) || p.JournalSlots < 0 || p.JournalSlots > PublicationFinalSlots {
		return fmt.Errorf("publication evidence differs")
	}
	u, _ := strconv.ParseUint(p.Owner, 10, 32)
	if p.StorageIdentity.Owner != uint32(u) || !p.StorageIdentity.BirthKnown || p.StorageIdentity.Inode == 0 || p.StorageIdentity.BornSeconds < 1 || p.StorageIdentity.BornNanoseconds < 0 || p.StorageIdentity.BornNanoseconds >= 1e9 {
		return fmt.Errorf("publication backing identity differs")
	}
	if p.WorkerPhase != "preparation" && p.WorkerPhase != "verification" {
		return fmt.Errorf("unknown native worker phase")
	}
	if p.WorkerPhase == "preparation" && (p.Worker != p.PreparationWorker || p.Publish != nil) {
		return fmt.Errorf("preparation evidence changed phase")
	}
	if p.WorkerPhase == "verification" {
		q := p.Publish
		if q == nil || q.Validate() != nil || q.Share != p.ID || q.Generation != p.Generation || q.Revision >= p.Revision || p.Summary == nil || q.PreparedSHA256 != p.Summary.SHA256 {
			return fmt.Errorf("verification lacks exact immutable intent")
		}
	}
	if p.State == "verifying" && p.WorkerPhase != "verification" || p.State == "preparing" && p.WorkerPhase != "preparation" || p.State == "prepared" && p.WorkerPhase != "preparation" || p.Published != nil && p.WorkerPhase != "verification" {
		return fmt.Errorf("publication/native phase differs")
	}
	switch p.WorkerState {
	case "reserved", "running", "joined", "unknown":
	default:
		return fmt.Errorf("unknown preparation worker state")
	}
	switch p.State {
	case "preparing", "verifying", "ending", "needs_attention":
	case "prepared", "published", "retiring", "retired", "cancelled", "failed":
		if p.WorkerState != "joined" {
			return fmt.Errorf("unjoined preparation")
		}
	default:
		return fmt.Errorf("unknown publication state")
	}
	if (p.Prepared == nil) != (p.Summary == nil) {
		return fmt.Errorf("prepared manifest relation differs")
	}
	for _, s := range []*time.Time{p.Prepared, p.Published, p.Retired} {
		if s != nil && (!stamp(*s) || s.Before(p.Created) || s.After(p.Updated)) {
			return fmt.Errorf("publication internal timeline differs")
		}
	}
	if p.Published != nil && (p.Prepared == nil || p.Published.Before(*p.Prepared)) {
		return fmt.Errorf("publication precedes preparation")
	}
	if p.Retired != nil && p.Published != nil && p.Retired.Before(*p.Published) {
		return fmt.Errorf("retirement precedes publication")
	}
	if p.State == "verifying" || p.State == "prepared" || p.State == "published" || p.State == "retiring" || p.State == "retired" {
		if p.Summary == nil {
			return fmt.Errorf("publication lacks manifest")
		}
	}
	if p.State == "published" || p.State == "retiring" || p.State == "retired" {
		if p.Published == nil {
			return fmt.Errorf("publication lacks exposure receipt")
		}
	}
	if p.State == "preparing" && (p.Prepared != nil || p.Published != nil || p.Retired != nil) {
		return fmt.Errorf("premature publication evidence")
	}
	if (p.State == "prepared" || p.State == "verifying") && (p.Published != nil || p.Retired != nil) {
		return fmt.Errorf("prepared data already exposed")
	}
	terminal := p.State == "retired" || p.State == "cancelled" || p.State == "failed"
	if terminal != (p.Retired != nil) || terminal && p.JournalSlots != 0 || !terminal && p.JournalSlots < 1 {
		return fmt.Errorf("publication settlement evidence differs")
	}
	if (p.State == "cancelled" || p.State == "failed") && p.Published != nil {
		return fmt.Errorf("exposed share cannot settle as unexposed preparation")
	}
	if !terminal && p.WorkerState != "joined" && p.State != "needs_attention" && p.JournalSlots < 2 {
		return fmt.Errorf("preparation lacks recovery plus finalization reservation")
	}
	if p.Summary != nil && p.Summary.Validate() != nil {
		return fmt.Errorf("invalid complete manifest summary")
	}
	return nil
}

type Control struct {
	Schema         int             `json:"schema"`
	Source         string          `json:"source"`
	SourceOwner    string          `json:"source_owner"`
	Target         string          `json:"target"`
	TargetOwner    string          `json:"target_owner"`
	Pair           string          `json:"pair"`
	Revision       int64           `json:"revision"`
	Certificate    string          `json:"certificate"`
	Share          string          `json:"share"`
	Generation     string          `json:"generation"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	Key            string          `json:"key"`
	Purpose        string          `json:"purpose"`
	Path           string          `json:"path"`
	Reason         string          `json:"reason"`
	TTL            time.Duration   `json:"ttl_ns"`
	Resources      model.Resources `json:"resources"`
}

func (q Control) Validate() error {
	if q.Schema != 1 || !id(q.Source) || !uid(q.SourceOwner) || !id(q.Target) || q.Source == q.Target || !uid(q.TargetOwner) || !digest(q.Pair) || q.Revision < 1 || !digest(q.Certificate) || !id(q.Share) || !id(q.Generation) || !digest(q.ManifestSHA256) || !key(q.Key) || q.Purpose != "file-read" || !relative(q.Path) || !text(q.Reason, 4096) || q.TTL <= 0 || q.TTL > 7*24*time.Hour || !resources(q.Resources) || q.Resources.CPU < 1 || q.Resources.Memory < 32<<20 || q.Resources.Disk != 0 {
		return fmt.Errorf("closed exact read-only consumer control required")
	}
	return nil
}
func (q Control) Digest() (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	return hash(q)
}

type Admission struct {
	Schema  int       `json:"schema"`
	Control Control   `json:"control"`
	Lease   string    `json:"lease"`
	At      time.Time `json:"at"`
	Expires time.Time `json:"expires_at"`
	Worker  Worker    `json:"worker"`
}

func (a Admission) Validate() error {
	if a.Schema != 1 || a.Control.Validate() != nil || !id(a.Lease) || !stamp(a.At) || !stamp(a.Expires) || !a.Expires.After(a.At) || a.Expires.After(a.At.Add(a.Control.TTL)) || a.Worker.Validate(a.Control.TargetOwner) != nil {
		return fmt.Errorf("consumer admission evidence differs")
	}
	return nil
}

type Consumer struct {
	Schema       int        `json:"schema"`
	Admission    Admission  `json:"admission"`
	State        string     `json:"state"`
	WorkerState  string     `json:"worker_state"`
	Revision     int64      `json:"revision"`
	Updated      time.Time  `json:"updated_at"`
	Ended        *time.Time `json:"ended_at,omitempty"`
	Outcome      string     `json:"outcome"`
	Reason       string     `json:"reason"`
	JournalSlots int        `json:"journal_slots"`
}

func (c Consumer) Validate() error {
	if c.Schema != 1 || c.Admission.Validate() != nil || c.Revision < 1 || !stamp(c.Updated) || c.Updated.Before(c.Admission.At) || !text(c.Reason, 4096) || c.JournalSlots < 0 || c.JournalSlots > ConsumerFinalSlots {
		return fmt.Errorf("consumer evidence differs")
	}
	switch c.State {
	case "admitted", "closing", "needs_attention", "terminal":
	default:
		return fmt.Errorf("unknown consumer state")
	}
	switch c.WorkerState {
	case "reserved", "running", "joined", "unknown":
	default:
		return fmt.Errorf("unknown consumer worker state")
	}
	if c.State == "terminal" {
		if c.WorkerState != "joined" || c.Ended == nil || !stamp(*c.Ended) || c.Ended.Before(c.Admission.At) || c.Ended.After(c.Updated) || c.JournalSlots != 0 {
			return fmt.Errorf("consumer falsely settled")
		}
		switch c.Outcome {
		case "released", "expired", "revoked", "cancelled", "interrupted", "failed":
		default:
			return fmt.Errorf("unknown consumer outcome")
		}
	} else if c.Ended != nil || c.JournalSlots < 1 {
		return fmt.Errorf("nonterminal consumer has final timestamp")
	}
	if c.State != "terminal" && c.WorkerState != "joined" && c.State != "needs_attention" && c.JournalSlots < 2 {
		return fmt.Errorf("consumer lacks recovery plus finalization reservation")
	}
	if c.State == "admitted" && (c.Outcome != "" || c.WorkerState != "reserved" && c.WorkerState != "running") {
		return fmt.Errorf("admitted consumer has ending evidence")
	}
	if c.Outcome != "" {
		switch c.Outcome {
		case "released", "expired", "revoked", "cancelled", "interrupted", "failed":
		default:
			return fmt.Errorf("unknown consumer outcome")
		}
	}
	return nil
}

type Fence struct {
	Schema          int       `json:"schema"`
	Control         Control   `json:"control"`
	ControlSHA256   string    `json:"control_sha256"`
	State           string    `json:"state"`
	Lease           string    `json:"lease"`
	AdmissionSHA256 string    `json:"admission_sha256"`
	At              time.Time `json:"at"`
	Actor           string    `json:"actor"`
	Reason          string    `json:"reason"`
}

func (f Fence) Validate() error {
	h, err := f.Control.Digest()
	if err != nil || f.Schema != 1 || f.ControlSHA256 != h || !stamp(f.At) || !text(f.Actor, 160) || !text(f.Reason, 4096) {
		return fmt.Errorf("exact consumer fence required")
	}
	switch f.State {
	case "sealed":
		if f.Lease != "" || f.AdmissionSHA256 != "" {
			return fmt.Errorf("sealed key carries admission")
		}
	case "admitted":
		if !id(f.Lease) || !digest(f.AdmissionSHA256) {
			return fmt.Errorf("admitted key lacks lease commitment")
		}
	default:
		return fmt.Errorf("unknown consumer fence state")
	}
	return nil
}

func DecodePublication(raw []byte) (Publication, error) {
	var v Publication
	err := decode(raw, &v)
	if err == nil {
		err = v.Validate()
	}
	return v, err
}
func DecodeConsumer(raw []byte) (Consumer, error) {
	var v Consumer
	err := decode(raw, &v)
	if err == nil {
		err = v.Validate()
	}
	return v, err
}
func DecodeFence(raw []byte) (Fence, error) {
	var v Fence
	err := decode(raw, &v)
	if err == nil {
		err = v.Validate()
	}
	return v, err
}
func Canonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err == nil && len(b) > MaxRecordBytes {
		return nil, fmt.Errorf("share record bound exceeded")
	}
	return b, err
}
func hash(v any) (string, error) {
	raw, err := Canonical(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
func decode(raw []byte, out any) error {
	if len(raw) > MaxRecordBytes {
		return fmt.Errorf("share record bound exceeded")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing share evidence")
	}
	b, err := Canonical(out)
	if err != nil || !bytes.Equal(raw, b) {
		return fmt.Errorf("share evidence must retain exact canonical producer fields")
	}
	return nil
}
func id(s string) bool     { return hexText(s, 16) }
func digest(s string) bool { return hexText(s, 32) }
func hexText(s string, n int) bool {
	if len(s) != n*2 || strings.ToLower(s) != s {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n
}
func uid(s string) bool {
	n, e := strconv.ParseUint(s, 10, 32)
	return e == nil && strconv.FormatUint(n, 10) == s
}
func text(s string, n int) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > n {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func key(s string) bool {
	if !text(s, 128) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func resources(r model.Resources) bool {
	return !math.IsNaN(r.CPU) && !math.IsInf(r.CPU, 0) && r.CPU >= 0 && r.CPU <= 65536 && r.Memory >= 0 && r.Disk >= 0
}
func stamp(t time.Time) bool { return !t.IsZero() && t.Location() == time.UTC }
func relative(p string) bool {
	if p == "" || len(p) > 4096 || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) > 32 {
		return false
	}
	for _, c := range parts {
		if c == "" || c == "." || c == ".." || len(c) > 255 || !text(c, 255) {
			return false
		}
	}
	return true
}

func ValidateReason(reason string) error {
	if !text(reason, 4096) {
		return fmt.Errorf("required valid UTF-8 reason up to4096bytes")
	}
	return nil
}
