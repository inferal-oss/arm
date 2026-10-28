package database

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

var proofForwardUnixSocket = regexp.MustCompile(`^(forward-|f-)[a-f0-9]{32}\.sock$`)

type proofForwardUnixNoBind struct {
	Directory model.FileIdentity `json:"directory"`
	Allocated int64              `json:"allocated_bytes"`
	At        time.Time          `json:"at"`
}
type proofForwardUnixIntent struct {
	Directory model.FileIdentity `json:"directory"`
	Allocated int64              `json:"allocated_bytes"`
	At        time.Time          `json:"at"`
}
type proofForwardUnixBound struct {
	Socket    model.FileIdentity `json:"socket"`
	Directory model.FileIdentity `json:"directory"`
	Allocated int64              `json:"allocated_bytes"`
	At        time.Time          `json:"at"`
}
type proofForwardUnixNativeTerminal struct {
	Socket    model.FileIdentity `json:"socket"`
	Directory model.FileIdentity `json:"directory"`
	Allocated int64              `json:"allocated_bytes"`
	At        time.Time          `json:"at"`
}
type proofForwardUnixPair struct {
	Schema          int                             `json:"schema"`
	SourceID        string                          `json:"source_id"`
	SourceKey       string                          `json:"source_key"`
	SourceBinding   model.ForwardSourceBinding      `json:"source_binding"`
	SourceClock     leaseclock.Deadline             `json:"source_clock"`
	SourceRevision  int                             `json:"source_revision"`
	StorageID       string                          `json:"storage_id"`
	StorageKey      string                          `json:"storage_key"`
	StorageClock    leaseclock.Deadline             `json:"storage_clock"`
	StorageRevision int                             `json:"storage_revision"`
	StorageArtifact model.FileIdentity              `json:"storage_artifact"`
	StorageRoot     model.FileIdentity              `json:"storage_root"`
	DiskBytes       int64                           `json:"disk_bytes"`
	SocketName      string                          `json:"socket_name"`
	Phase           string                          `json:"phase"`
	Revision        int                             `json:"revision"`
	NoBind          *proofForwardUnixNoBind         `json:"no_bind,omitempty"`
	BindIntent      *proofForwardUnixIntent         `json:"bind_intent,omitempty"`
	Bound           *proofForwardUnixBound          `json:"bound,omitempty"`
	NativeTerminal  *proofForwardUnixNativeTerminal `json:"native_terminal,omitempty"`
}

// A terminal no-bind tombstone is inspectable history, not a live resource
// consumer. Reset still requires the ordinary source, port and storage cleanup
// proofs. All nonterminal phases remain explicit blockers.
func (p *proofReader) readForwardUnixPairs() error {
	return p.walk("forward_unix_pairs", func(r proofRow) error {
		sourceID, err := r.text("source_id")
		if err != nil {
			return err
		}
		phase, err := r.text("phase")
		if err != nil {
			return err
		}
		if phase != "terminal" {
			return p.block("forward_unix_pair", sourceID, "Forward Unix pair requires original socket/storage cleanup proof")
		}
		raw, err := r.text("data")
		if err != nil {
			return err
		}
		var q proofForwardUnixPair
		keys := []string{"schema", "source_id", "source_key", "source_binding", "source_clock", "source_revision", "storage_id", "storage_key", "storage_clock", "storage_revision", "storage_artifact", "storage_root", "disk_bytes", "socket_name", "phase", "revision"}
		var shape map[string]json.RawMessage
		if err = json.Unmarshal([]byte(raw), &shape); err != nil {
			return err
		}
		if _, native := shape["native_terminal"]; native {
			keys = append(keys, "bind_intent", "bound", "native_terminal")
		} else {
			keys = append(keys, "no_bind")
		}
		if err = strictEvidence(raw, &q, keys...); err != nil {
			return err
		}
		canonical, err := json.Marshal(q)
		if err != nil || string(canonical) != raw {
			return fmt.Errorf("noncanonical forward Unix pair tombstone")
		}
		storageID, err := r.text("storage_id")
		if err != nil {
			return err
		}
		sourceKey, err := r.text("source_key")
		if err != nil {
			return err
		}
		storageKey, err := r.text("storage_key")
		if err != nil {
			return err
		}
		name, err := r.text("socket_name")
		if err != nil {
			return err
		}
		disk, err := r.integer("disk_bytes")
		if err != nil {
			return err
		}
		source, sourceExists := p.leases[sourceID]
		storage, storageExists := p.leases[storageID]
		noBind := q.Revision == 2 && q.NoBind != nil && q.BindIntent == nil && q.Bound == nil && q.NativeTerminal == nil && q.NoBind.Directory == q.StorageArtifact && q.NoBind.Allocated >= 0 && q.NoBind.Allocated <= disk && !q.NoBind.At.IsZero() && q.NoBind.At.Location() == time.UTC
		native := q.Revision == 4 && q.NoBind == nil && q.BindIntent != nil && q.Bound != nil && q.NativeTerminal != nil && q.BindIntent.Directory == q.StorageArtifact && q.BindIntent.Allocated >= 0 && q.BindIntent.Allocated <= disk && !q.BindIntent.At.IsZero() && q.BindIntent.At.Location() == time.UTC && q.Bound.Directory == q.StorageArtifact && q.Bound.Socket.Device == q.StorageArtifact.Device && q.Bound.Socket.Inode != 0 && q.Bound.Socket.BirthKnown && q.Bound.Allocated >= 0 && q.Bound.Allocated <= disk && !q.Bound.At.IsZero() && q.Bound.At.Location() == time.UTC && q.NativeTerminal.Socket == q.Bound.Socket && q.NativeTerminal.Directory == q.StorageArtifact && q.NativeTerminal.Allocated >= 0 && q.NativeTerminal.Allocated <= disk && !q.NativeTerminal.At.IsZero() && q.NativeTerminal.At.Location() == time.UTC
		if native {
			native = q.Bound.Socket.Owner == q.StorageArtifact.Owner && !q.Bound.At.Before(q.BindIntent.At) && !q.NativeTerminal.At.Before(q.Bound.At)
		}
		if !sourceExists || !storageExists || q.Schema != 1 || q.Phase != phase || !(noBind || native) || q.SourceID != sourceID || q.SourceKey != sourceKey || q.StorageID != storageID || q.StorageKey != storageKey || q.SocketName != name || q.DiskBytes != disk || disk != 4<<20 || !proofForwardUnixSocket.MatchString(name) || p.result.StorageRoot == nil || q.StorageRoot != *p.result.StorageRoot || source.Request.Kind != "forward-listener" || source.Request.Key != sourceKey || source.Request.ForwardSource == nil || !reflect.DeepEqual(*source.Request.ForwardSource, q.SourceBinding) || source.Request.StorageLease != "" || source.Clock == nil || *source.Clock != q.SourceClock || source.Revision <= q.SourceRevision || storage.Request.Kind != "storage" || storage.Request.Key != storageKey || storage.Request.Resources != (model.Resources{Disk: disk}) || storage.Request.Project != "mesh-forward" || storage.Request.Reason != "Forward Unix source directory for "+sourceID || storage.Clock == nil || *storage.Clock != q.StorageClock || storage.Revision < q.StorageRevision || storage.Artifact == nil || storage.Artifact.Identity == nil || *storage.Artifact.Identity != q.StorageArtifact || source.Host != storage.Host || source.Owner != storage.Owner {
			return fmt.Errorf("forward Unix no-bind tombstone contradicts protected leases")
		}
		if source.State != "terminal" || storage.State != "terminal" || storage.Artifact == nil || storage.Artifact.Phase != "removed" || !storage.Artifact.NamespaceRemoved || storage.Artifact.AllocatedBefore == nil || *storage.Artifact.AllocatedBefore != 0 {
			return p.block("forward_unix_pair", sourceID, "No-bind pair has unsettled source or storage obligations")
		}
		return nil
	})
}
