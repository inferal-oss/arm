package kubejournal

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON applies the closed codec even when the record is embedded in
// the protected mesh coordination file's otherwise legacy decoder.
func (r *Record) UnmarshalJSON(raw []byte) error {
	type plain Record
	var next plain
	if err := Decode(raw, &next); err != nil {
		return err
	}
	value := Record(next)
	if err := value.Validate(); err != nil {
		return err
	}
	*r = value
	return nil
}
func (r Record) Digest() string {
	return Hash(struct {
		Schema, Key, Kind, Owner, Host, Plan string
		Connection                           *ConnectionDraft
	}{Schema, r.Key, r.Kind, r.Owner, r.SourceHost, r.Plan.SHA256, r.Connection})
}

// ValidateSuccessor prevents a generic coordination mutation from erasing
// prior approval, attempts or source obligations. It never authorizes effects.
func (r Record) ValidateSuccessor(next Record) error {
	if r.Validate() != nil || next.Validate() != nil || r.Digest() != next.Digest() || !r.Created.Equal(next.Created) || next.Revision != r.Revision+1 || next.Updated.Before(r.Updated) || len(next.Sources) < len(r.Sources) {
		return fmt.Errorf("Kubernetes immutable workflow or revision changed")
	}
	for n, old := range r.Stages {
		v := next.Stages[n]
		if old.State == "confirmed" || old.State == "declined" {
			if !same(old, v) {
				return fmt.Errorf("settled stage outcome replaced")
			}
			continue
		}
		if old.Approved && (!v.Approved || v.ApprovedRevision != old.ApprovedRevision || v.ApprovedSHA256 != old.ApprovedSHA256 || !same(v.Review, old.Review) || v.ExpectedGeneration != old.ExpectedGeneration) {
			return fmt.Errorf("prior stage approval replaced")
		}
		if !old.Attempted.IsZero() && !v.Attempted.Equal(old.Attempted) {
			return fmt.Errorf("prior effect attempt replaced")
		}
		if old.State == "unknown" && v.State != "unknown" && v.State != "confirmed" {
			return fmt.Errorf("uncertain stage became replayable")
		}
	}
	for n, old := range r.Sources {
		v := next.Sources[n]
		if old.Scope != v.Scope || old.Stage != v.Stage || !old.Intent.Equal(v.Intent) || old.Binding != nil && !same(old.Binding, v.Binding) || old.Result != nil && (!same(old.Result, v.Result) || !old.Finished.Equal(v.Finished)) {
			return fmt.Errorf("prior source evidence replaced")
		}
	}
	if r.Phase == "cancelled" && next.Phase != "cancelled" || r.Phase == "declined" && next.Phase != "declined" || r.Phase == "revoked" && next.Phase != "revoked" {
		return fmt.Errorf("prior stop decision erased")
	}
	return nil
}

// Snapshot returns a detached strict copy, for use before atomic native writes.
func (r Record) Snapshot() (Record, error) {
	raw, err := json.Marshal(r)
	var out Record
	if err == nil {
		err = Decode(raw, &out)
	}
	return out, err
}
