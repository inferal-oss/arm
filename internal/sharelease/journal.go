package sharelease

import (
	"fmt"
	"time"
)

// Journal is the bounded immutable policy record, separate from observational
// telemetry. The SQL sequence is an outer monotonically increasing cursor. Each
// entity revision commits exact before/after producer bytes, not a guessed path.
type Journal struct {
	Schema       int       `json:"schema"`
	Authority    string    `json:"authority"`
	Owner        string    `json:"owner"`
	At           time.Time `json:"at"`
	Actor        string    `json:"actor"`
	Reason       string    `json:"reason"`
	Entity       string    `json:"entity"`
	ID           string    `json:"id"`
	Revision     int64     `json:"revision"`
	Transition   string    `json:"transition"`
	BeforeSHA256 string    `json:"before_sha256"`
	AfterSHA256  string    `json:"after_sha256"`
}

func (j Journal) Validate() error {
	if j.Schema != 1 || !id(j.Authority) || !uid(j.Owner) || !stamp(j.At) || !text(j.Actor, 160) || !text(j.Reason, 4096) || j.Revision < 1 || !digest(j.AfterSHA256) || j.Revision == 1 && j.BeforeSHA256 != "" || j.Revision > 1 && !digest(j.BeforeSHA256) {
		return fmt.Errorf("exact bounded policy journal evidence required")
	}
	switch j.Entity {
	case "publication":
		if !id(j.ID) {
			return fmt.Errorf("publication identity differs")
		}
		switch j.Transition {
		case "reserved", "worker_started", "prepared", "published", "ending", "worker_joined", "needs_attention", "retiring", "retired", "cancelled", "failed":
		default:
			return fmt.Errorf("unknown publication transition")
		}
	case "consumer":
		if !id(j.ID) {
			return fmt.Errorf("consumer lease identity differs")
		}
		switch j.Transition {
		case "admitted", "worker_started", "closing", "needs_attention", "terminal":
		default:
			return fmt.Errorf("unknown consumer transition")
		}
	case "fence":
		if !digest(j.ID) || j.Revision != 1 || j.Transition != "sealed" && j.Transition != "admitted" {
			return fmt.Errorf("immutable exact key fence commitment required")
		}
	default:
		return fmt.Errorf("unknown share journal entity")
	}
	return nil
}
func EncodeJournal(j Journal) ([]byte, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	raw, err := Canonical(j)
	if err == nil && len(raw) > MaxJournalRecordBytes {
		return nil, fmt.Errorf("share journal record bound exceeded")
	}
	return raw, err
}
func DecodeJournal(raw []byte) (Journal, error) {
	var j Journal
	if len(raw) > MaxJournalRecordBytes {
		return j, fmt.Errorf("share journal record bound exceeded")
	}
	if err := decode(raw, &j); err != nil {
		return j, err
	}
	return j, j.Validate()
}

// JournalCapacity accounts for worst-case REQUIRED future entries BEFORE native
// work. No rejected over-cap mutation, cancellation or retry can silently consume
// finalization space. Snapshot construction supplies exact retained bytes/slots.
type JournalCapacity struct {
	Rows          int
	Bytes         int64
	ReservedSlots int
}

func (c JournalCapacity) Validate() error {
	if c.Rows < 0 || c.Rows > MaxJournalRows || c.Bytes < 0 || c.Bytes > MaxJournalBytes || c.ReservedSlots < 0 || c.ReservedSlots > MaxJournalRows || c.Rows+c.ReservedSlots > MaxJournalRows || c.Bytes+int64(c.ReservedSlots)*MaxJournalRecordBytes > MaxJournalBytes {
		return fmt.Errorf("retained share journal/finalization capacity exceeded")
	}
	return nil
}

// Change returns a pure capacity plan. The manager must commit both the exact
// event and changed entity slots in the SAME transaction as the ordinary lease.
// oldSlots must come from the current exact reviewed entity. New entity old=0;
// ordinary transitions consume at least one reserved entry. A separately
// admitted operator recovery attempt may reserve additional slots explicitly.
func (c JournalCapacity) Change(eventBytes, oldSlots, newSlots int) (JournalCapacity, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if eventBytes < 1 || eventBytes > MaxJournalRecordBytes || oldSlots < 0 || oldSlots > c.ReservedSlots || newSlots < 0 || newSlots > PublicationFinalSlots {
		return c, fmt.Errorf("invalid exact journal capacity change")
	}
	next := JournalCapacity{Rows: c.Rows + 1, Bytes: c.Bytes + int64(eventBytes), ReservedSlots: c.ReservedSlots - oldSlots + newSlots}
	if err := next.Validate(); err != nil {
		return c, err
	}
	return next, nil
}

// ManifestReservation uses the maximum COMPLETE manifest size for an admitted
// preparation until its exact result is known. Terminal never-published failures
// with no manifest release their unused reservation; retained manifests are not
// purged just because a publication retires.
func ManifestReservation(p Publication) (raw, stored int64, err error) {
	if err := p.Validate(); err != nil {
		return 0, 0, err
	}
	if p.Summary != nil {
		return int64(p.Summary.RawBytes), int64(p.Summary.StoredBytes), nil
	}
	if p.Retired != nil {
		return 0, 0, nil
	}
	raw = ManifestPartBytes * MaxManifestParts // one full8MiB capability, not a caller claim
	parts := int(raw) / ManifestPartBytes
	return raw, int64(base64Size(int(raw), parts)), nil
}
