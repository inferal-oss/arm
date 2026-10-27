// Package syncservice owns the exact-key synchronization operation and receipt
// contract between ARM's leased SSH worker, syncreconcile, and syncfs. It does
// not open SSH channels, reserve Manager resources, or mutate selected files.
package syncservice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
)

const (
	// FixedWorkerCommand is the exact versioned SSH exec commitment shared by
	// Manager admission and the private worker. It does not enable a CLI entry.
	FixedWorkerCommand = "arm sync-worker --protocol=1"
	Schema             = 1
	MaxRecordBytes     = 64 << 20
	MaxManifestBytes   = 64 << 20
)

var (
	ErrKeyExists   = errors.New("sync key already reserved; inspect the original key")
	ErrUnknown     = errors.New("sync outcome unknown; inspect the original key without replay")
	ErrUnconfirmed = errors.New("both exact endpoint completion receipts are required")
)

// ManifestRow records either an observed selected entry or an absent candidate
// name. The latter binds native case/Unicode and exclusion checks at both ends.
type ManifestRow struct {
	Side    syncreconcile.Side   `json:"side"`
	Path    string               `json:"path"`
	NameKey string               `json:"name_key"`
	Entry   *syncreconcile.Entry `json:"entry,omitempty"`
}

// Intent is reserved durably at the source before SSH dispatch and at the
// destination in the same transaction as leased SSH admission. Local is the
// initiating endpoint; remote is the SSH destination.
type Intent struct {
	Schema              int                             `json:"schema"`
	Key                 string                          `json:"key"`
	SSHKey              string                          `json:"ssh_key"`
	WorkerCommandSHA256 string                          `json:"worker_command_sha256"`
	SSHControlSHA256    string                          `json:"ssh_control_sha256"`
	Reason              string                          `json:"reason"`
	Side                syncreconcile.Side              `json:"side"`
	Binding             syncreconcile.Binding           `json:"binding"`
	Direction           syncreconcile.Direction         `json:"direction"`
	LocalPolicy         syncreconcile.Policy            `json:"local_policy"`
	RemotePolicy        syncreconcile.Policy            `json:"remote_policy"`
	PlanDigest          string                          `json:"plan_digest"`
	Generation          uint64                          `json:"generation"`
	PriorSeal           string                          `json:"prior_seal,omitempty"`
	ManifestDigest      string                          `json:"manifest_digest"`
	LocalResources      model.Resources                 `json:"local_resources"`
	RemoteResources     model.Resources                 `json:"remote_resources"`
	Actions             []syncreconcile.Action          `json:"actions"`
	Resolutions         []syncreconcile.Resolution      `json:"resolutions,omitempty"`
	Proposal            syncreconcile.ConfirmedBaseline `json:"proposal"`
	Created             time.Time                       `json:"created"`
}

// NewIntent recalculates the reviewed plan, so an unrelated Plan cannot be
// paired with different observations. A caller supplies the native snapshots,
// reviewed resource promises, and one exact SSH execution key.
func NewIntent(key, workerCommandSHA256, sshControlSHA256, reason string, side syncreconcile.Side, req syncreconcile.Request, reviewed syncreconcile.Plan, local, remote model.Resources, created time.Time) (Intent, []ManifestRow, error) {
	if side != syncreconcile.Local && side != syncreconcile.Remote {
		return Intent{}, nil, errors.New("invalid endpoint side")
	}
	if err := resources(local); err != nil {
		return Intent{}, nil, fmt.Errorf("local resources: %w", err)
	}
	if err := resources(remote); err != nil {
		return Intent{}, nil, fmt.Errorf("remote resources: %w", err)
	}
	actual, err := syncreconcile.Reconcile(req)
	if err != nil {
		return Intent{}, nil, err
	}
	if actual.Digest() != reviewed.Digest() || actual.Generation() != reviewed.Generation() || len(actual.Conflicts()) != 0 {
		return Intent{}, nil, errors.New("reviewed plan changed or has unresolved conflicts")
	}
	proposal, ok := actual.ProposedBaseline()
	if !ok {
		return Intent{}, nil, errors.New("missing baseline proposal")
	}
	manifestLimit := min(int64(MaxManifestBytes), local.Memory/4, remote.Memory/4)
	rows, err := manifestRowsBounded(req.Local, req.Remote, manifestLimit)
	if err != nil {
		return Intent{}, nil, err
	}
	manifestRaw, err := json.Marshal(rows)
	if err != nil {
		return Intent{}, nil, err
	}
	if len(manifestRaw) > MaxManifestBytes {
		return Intent{}, nil, errors.New("selected manifest exceeds byte bound")
	}
	priorSeal := ""
	if req.Baseline != nil {
		priorSeal = req.Baseline.Seal
	}
	i := Intent{Schema: Schema, Key: key, SSHKey: key, WorkerCommandSHA256: workerCommandSHA256, SSHControlSHA256: sshControlSHA256, Reason: reason, Side: side, Binding: proposal.Binding, Direction: req.Direction, LocalPolicy: req.LocalPolicy, RemotePolicy: req.RemotePolicy, PlanDigest: actual.Digest(), Generation: actual.Generation(), PriorSeal: priorSeal, ManifestDigest: sha(manifestRaw), LocalResources: local, RemoteResources: remote, Actions: actual.Actions(), Resolutions: cloneResolutions(req.Resolutions), Proposal: proposal, Created: created.UTC()}
	if err = i.Validate(); err != nil {
		return Intent{}, nil, err
	}
	recordRaw, err := json.Marshal(i)
	if err != nil {
		return Intent{}, nil, err
	}
	if err = validateFootprint(i, len(manifestRaw), len(recordRaw)); err != nil {
		return Intent{}, nil, err
	}
	return i, rows, nil
}

func (i Intent) Validate() error {
	if i.Schema != Schema || !validKey(i.Key) || i.SSHKey != i.Key || !hexSHA(i.WorkerCommandSHA256) || !hexSHA(i.SSHControlSHA256) || len(i.Reason) == 0 || len(i.Reason) > 4096 || i.Side != syncreconcile.Local && i.Side != syncreconcile.Remote {
		return errors.New("invalid sync operation identity")
	}
	if !hexSHA(i.PlanDigest) || !hexSHA(i.ManifestDigest) || i.Generation == 0 || i.Generation != i.Proposal.Generation || !reflect.DeepEqual(i.Binding, i.Proposal.Binding) || !hexSHA(i.Proposal.Seal) {
		return errors.New("invalid plan or baseline binding")
	}
	if i.Generation == 1 {
		if i.PriorSeal != "" {
			return errors.New("first generation has prior seal")
		}
	} else if !hexSHA(i.PriorSeal) {
		return errors.New("confirmed prior baseline seal required")
	}
	if i.Direction != syncreconcile.Push && i.Direction != syncreconcile.Pull && i.Direction != syncreconcile.Both {
		return errors.New("invalid direction")
	}
	if i.LocalPolicy != syncreconcile.Additive && i.LocalPolicy != syncreconcile.Full || i.RemotePolicy != syncreconcile.Additive && i.RemotePolicy != syncreconcile.Full {
		return errors.New("invalid receiving policy")
	}
	if err := resources(i.LocalResources); err != nil {
		return fmt.Errorf("local resources: %w", err)
	}
	if err := resources(i.RemoteResources); err != nil {
		return fmt.Errorf("remote resources: %w", err)
	}
	if i.Created.IsZero() || i.Created.Location() != time.UTC {
		return errors.New("UTC creation time required")
	}
	if len(i.Actions) > syncreconcile.MaxActions {
		return errors.New("too many actions")
	}
	if len(i.Resolutions) > syncreconcile.MaxResolutions {
		return errors.New("too many conflict resolutions")
	}
	for _, a := range i.Actions {
		if a.Side != syncreconcile.Local && a.Side != syncreconcile.Remote {
			return errors.New("invalid action side")
		}
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if len(raw) > MaxRecordBytes {
		return errors.New("operation record exceeds byte bound")
	}
	return nil
}

func cloneResolutions(in []syncreconcile.Resolution) []syncreconcile.Resolution {
	out := slices.Clone(in)
	for index := range out {
		if out[index].ExpectedLocal != nil {
			v := *out[index].ExpectedLocal
			out[index].ExpectedLocal = &v
		}
		if out[index].ExpectedRemote != nil {
			v := *out[index].ExpectedRemote
			out[index].ExpectedRemote = &v
		}
	}
	return out
}

// ReconcileIntent reconstructs the exact reviewed plan on the destination
// from the selected manifest and its locally confirmed baseline. It refuses a
// changed generation, resolution, native name key, or proposed result.
func ReconcileIntent(i Intent, rows []ManifestRow, baseline *syncreconcile.ConfirmedBaseline) (syncreconcile.Plan, error) {
	if err := i.Validate(); err != nil {
		return syncreconcile.Plan{}, err
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return syncreconcile.Plan{}, err
	}
	if len(raw) > MaxManifestBytes || sha(raw) != i.ManifestDigest {
		return syncreconcile.Plan{}, errors.New("manifest digest changed")
	}
	if err = validateManifest(i, rows); err != nil {
		return syncreconcile.Plan{}, err
	}
	if i.Generation == 1 {
		if baseline != nil {
			return syncreconcile.Plan{}, errors.New("unexpected prior baseline")
		}
	} else if baseline == nil || baseline.Generation+1 != i.Generation || baseline.Seal != i.PriorSeal {
		return syncreconcile.Plan{}, errors.New("prior baseline generation or seal changed")
	}
	local := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	remote := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	for _, row := range rows {
		target := &local
		if row.Side == syncreconcile.Remote {
			target = &remote
		}
		target.NameKeys[row.Path] = row.NameKey
		if row.Entry != nil {
			target.Entries[row.Path] = *row.Entry
		}
	}
	req := syncreconcile.Request{Binding: i.Binding, Direction: i.Direction, LocalPolicy: i.LocalPolicy, RemotePolicy: i.RemotePolicy, Local: local, Remote: remote, FirstRun: i.Generation == 1, Baseline: baseline, ExpectedGeneration: i.Generation - 1, Resolutions: cloneResolutions(i.Resolutions)}
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		return syncreconcile.Plan{}, err
	}
	proposal, ok := plan.ProposedBaseline()
	if !ok || len(plan.Conflicts()) != 0 || plan.Digest() != i.PlanDigest || plan.Generation() != i.Generation || !reflect.DeepEqual(plan.Actions(), i.Actions) || !reflect.DeepEqual(proposal, i.Proposal) {
		return syncreconcile.Plan{}, errors.New("reconstructed plan differs from reviewed operation")
	}
	return plan, nil
}

func resources(v model.Resources) error {
	if math.IsNaN(v.CPU) || math.IsInf(v.CPU, 0) || v.CPU <= 0 || v.Memory <= 0 || v.Disk <= 0 {
		return errors.New("positive finite CPU, memory and disk promises required")
	}
	return nil
}
func validKey(k string) bool {
	if len(k) < 16 || len(k) > 128 {
		return false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
func hexSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}
func sha(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func manifestRowsBounded(local, remote syncreconcile.Snapshot, limit int64) ([]ManifestRow, error) {
	out := make([]ManifestRow, 0)
	var bytes int64 = 2 // JSON array brackets
	for _, item := range []struct {
		side     syncreconcile.Side
		snapshot syncreconcile.Snapshot
	}{{syncreconcile.Local, local}, {syncreconcile.Remote, remote}} {
		for p, key := range item.snapshot.NameKeys {
			var entry *syncreconcile.Entry
			if e, ok := item.snapshot.Entries[p]; ok {
				v := e
				entry = &v
			}
			row := ManifestRow{Side: item.side, Path: p, NameKey: key, Entry: entry}
			encoded, err := json.Marshal(row)
			if err != nil {
				return nil, err
			}
			bytes += int64(len(encoded))
			if len(out) > 0 {
				bytes++
			}
			if bytes > limit {
				return nil, errors.New("selected manifest exceeds byte bound")
			}
			out = append(out, row)
		}
	}
	slices.SortFunc(out, func(a, b ManifestRow) int {
		if x := strings.Compare(string(a.Side), string(b.Side)); x != 0 {
			return x
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// validateFootprint enforces a lower bound for the control and native work
// against both endpoint reservations. The Manager must still charge actual
// CPU, resident memory, stage/quarantine overhead, and worker lifetime.
func validateFootprint(i Intent, manifestBytes, recordBytes int) error {
	control := int64(manifestBytes) + int64(recordBytes)
	for _, side := range []syncreconcile.Side{syncreconcile.Local, syncreconcile.Remote} {
		var data, receipts int64
		for _, a := range i.Actions {
			if a.Side != side {
				continue
			}
			receipts += 512
			if a.After != nil && a.After.Kind != syncreconcile.Directory {
				if a.After.Size > math.MaxInt64-data {
					return errors.New("stage byte count overflow")
				}
				data += a.After.Size
			}
			if a.Before != nil && a.Before.Kind != syncreconcile.Directory {
				if a.Before.Size > math.MaxInt64-data {
					return errors.New("preimage byte count overflow")
				}
				data += a.Before.Size
			}
		}
		resources := i.LocalResources
		if side == syncreconcile.Remote {
			resources = i.RemoteResources
		}
		if control > math.MaxInt64/4 || data > math.MaxInt64-control-receipts {
			return errors.New("reservation byte count overflow")
		}
		if resources.Memory < 4*control+128<<10 {
			return fmt.Errorf("%s memory promise does not cover control footprint", side)
		}
		if resources.Disk < data+control+receipts {
			return fmt.Errorf("%s disk promise does not cover staged/preimage/control lower bound", side)
		}
	}
	return nil
}

// SideReceipt is emitted only after all of one endpoint's expected native
// effects have durable applied outcomes and its selected root is rechecked.
// The receiving Manager must authenticate the peer carrying the other side's
// receipt; this struct is not itself a signature.
type SideReceipt struct {
	Key            string             `json:"key"`
	PlanDigest     string             `json:"plan_digest"`
	Generation     uint64             `json:"generation"`
	Side           syncreconcile.Side `json:"side"`
	RootID         string             `json:"root_id"`
	ManifestDigest string             `json:"manifest_digest"`
	BaselineSeal   string             `json:"baseline_seal"`
	EffectCount    int                `json:"effect_count"`
	EffectsDigest  string             `json:"effects_digest"`
}

func expectedRoot(i Intent, side syncreconcile.Side) string {
	if side == syncreconcile.Local {
		return i.Binding.Local.RootID
	}
	return i.Binding.Remote.RootID
}
func expectedActions(i Intent, side syncreconcile.Side) []syncreconcile.Action {
	var out []syncreconcile.Action
	for _, a := range i.Actions {
		if a.Side == side {
			out = append(out, a)
		}
	}
	return out
}
func (r SideReceipt) matches(i Intent, side syncreconcile.Side) bool {
	return r.Key == i.Key && r.PlanDigest == i.PlanDigest && r.Generation == i.Generation && r.Side == side && r.RootID == expectedRoot(i, side) && r.ManifestDigest == i.ManifestDigest && r.BaselineSeal == i.Proposal.Seal && r.EffectCount == len(expectedActions(i, side)) && hexSHA(r.EffectsDigest)
}
