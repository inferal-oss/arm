package snapshotlease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/yrashk/arm/internal/model"
)

func LeaseKey(authority, key, role string) string {
	return Hash([]byte("arm.snapshot-reservation/v1\x00" + authority + "\x00" + role + "\x00" + key))
}
func Requests(q Request, authority string) (model.Request, model.Request) {
	work := model.Request{Kind: WorkKind, Key: LeaseKey(authority, q.Key, "work"), StorageLease: q.Source, Resources: q.Resources, Reason: q.Reason, TTL: q.WorkTTL, Budget: q.Budget}
	destination := model.Request{Kind: "storage", Key: LeaseKey(authority, q.Key, "destination"), Resources: model.Resources{Disk: q.ContentCeiling + (64 << 20)}, Reason: q.Reason, TTL: q.Retention, Budget: q.Budget}
	return work, destination
}

func readLease(ctx context.Context, tx *sql.Tx, id string) (l model.Lease, e error) {
	var raw []byte
	var selected, keyType, runType string
	var key sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(id)='text' AND length(CAST(id AS BLOB))=32 THEN id END,typeof(request_key),CASE WHEN typeof(request_key)='text' AND length(CAST(request_key AS BLOB)) BETWEEN 1 AND 128 THEN request_key END,CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 1048576 THEN data END,typeof(run) FROM leases WHERE id=?`, id).Scan(&selected, &keyType, &key, &raw, &runType)
	if e != nil {
		return l, e
	}
	if len(raw) == 0 {
		return l, fmt.Errorf("snapshot ordinary lease byte/type bound")
	}
	if e = json.Unmarshal(raw, &l); e != nil {
		return l, e
	}
	if selected != id || l.ID != id || runType != "null" || keyType != "text" || !key.Valid || key.String != l.Request.Key {
		return l, fmt.Errorf("snapshot ordinary lease SQL/execution binding differs")
	}
	return l, nil
}

// ValidateLeases uses the same SQL snapshot as the optional record reader.
// No path is opened and no PID, creating artifact or data row becomes authority.
func ValidateLeases(ctx context.Context, tx *sql.Tx, s Snapshot, home string) error {
	claimed := map[string]bool{}
	var provider model.FileIdentity
	if len(s.Copies) > 0 {
		var raw []byte
		if e := tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB)) BETWEEN 1 AND 4096 THEN value END FROM metadata WHERE key='storage_root'`).Scan(&raw); e != nil {
			return e
		}
		if len(raw) == 0 {
			return fmt.Errorf("snapshot provider evidence byte bound")
		}
		if e := json.Unmarshal(raw, &provider); e != nil {
			return e
		}
	}
	for id, r := range s.Copies {
		q := s.Requests[id]
		wantW, wantD := Requests(q, r.Authority)
		if provider != r.ProviderIdentity {
			return fmt.Errorf("snapshot recorded provider identity differs")
		}
		work, e := readLease(ctx, tx, r.WorkLease)
		if e != nil {
			return e
		}
		destination, e := readLease(ctx, tx, r.DestinationLease)
		if e != nil {
			return e
		}
		for _, pair := range []struct {
			l    model.Lease
			want model.Request
		}{{work, wantW}, {destination, wantD}} {
			l := pair.l
			var bindingRaw []byte
			if e := tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 16384 THEN data END FROM lease_budget_bindings WHERE lease=?`, l.ID).Scan(&bindingRaw); e != nil {
				return e
			}
			var binding model.BudgetBinding
			if len(bindingRaw) == 0 {
				return fmt.Errorf("snapshot budget binding bound")
			}
			if e := json.Unmarshal(bindingRaw, &binding); e != nil {
				return e
			}
			if l.Budget == nil || !reflect.DeepEqual(binding, *l.Budget) || binding.Authority != r.Authority || binding.Owner != r.Owner || binding.Selected != q.Budget {
				return fmt.Errorf("snapshot budget binding differs")
			}
			if l.Host != r.Authority || l.Owner != r.Owner || !reflect.DeepEqual(l.Capabilities, []string{"snapshot-reservation-no-work"}) || !reflect.DeepEqual(l.Request, pair.want) || !l.Created.Equal(r.Created) || l.Started == nil || !l.Started.Equal(r.Created) || l.Expires == nil || l.PID != 0 || l.Cargo != nil || l.Workload != nil || l.Budget == nil {
				return fmt.Errorf("snapshot ordinary lease immutable scope differs")
			}
		}
		if !work.Expires.Equal(r.WorkExpires) || !destination.Expires.Equal(r.DestinationExpires) || work.Artifact != nil || work.ManagedDir != "" || destination.Artifact == nil || destination.Artifact.Name != r.DestinationLease || destination.Artifact.Phase != "creating" || destination.Artifact.Policy != "snapshot-reservation-no-work/v1; no allocation attempted" || destination.Artifact.Identity != nil || destination.ManagedDir != filepath.Join(home, "storage", r.DestinationLease) || destination.Artifact.Path != destination.ManagedDir {
			return fmt.Errorf("snapshot planned allocation/expiry differs")
		}
		if r.Phase == "reserved-no-work" {
			if work.State != "active" || destination.State != "provisioning" || work.Revision != 1 || destination.Revision != 1 || work.Ended != nil || destination.Ended != nil || work.Outcome != "" || destination.Outcome != "" {
				return fmt.Errorf("snapshot unstarted promises are not both charged")
			}
		} else if work.State != "terminal" || destination.State != "terminal" || work.Revision != 2 || destination.Revision != 2 || work.Ended == nil || destination.Ended == nil || !work.Ended.Equal(r.Updated) || !destination.Ended.Equal(r.Updated) || work.Outcome != "cancelled" || destination.Outcome != "cancelled" {
			return fmt.Errorf("snapshot no-work settlement differs")
		}
		source, e := ReadSourceLease(ctx, tx, r.Source, r.Authority, r.Owner, home)
		if e != nil {
			return e
		}
		if source.ID != r.Source || source.Host != r.Authority || source.Owner != r.Owner || source.Request.Kind != "storage" || source.Artifact == nil || source.Artifact.Identity == nil || *source.Artifact.Identity != r.SourceIdentity || source.Revision < r.SourceRevision {
			return fmt.Errorf("snapshot retained source relationship differs")
		}
		if r.Phase == "reserved-no-work" && (source.Revision != r.SourceRevision || source.State != "active" || source.Artifact.Phase != "owned" || source.Expires == nil || !source.Expires.After(r.WorkExpires)) {
			return fmt.Errorf("snapshot source authority/dependency differs")
		}
		claimed[r.WorkLease] = true
		claimed[r.DestinationLease] = true
	}
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN typeof(id)='text' AND length(CAST(id AS BLOB))=32 THEN id END FROM leases WHERE CASE WHEN typeof(data)<>'text' OR length(CAST(data AS BLOB)) NOT BETWEEN 1 AND 1048576 THEN 1 WHEN NOT json_valid(data) THEN 1 ELSE json_extract(data,'$.request.kind')=? OR json_extract(data,'$.artifact.policy')='snapshot-reservation-no-work/v1; no allocation attempted' OR EXISTS (SELECT 1 FROM json_each(data,'$.capabilities') WHERE value='snapshot-reservation-no-work') END LIMIT 33`, WorkKind)
	if e != nil {
		return e
	}
	count := 0
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		count++
		if count > 2*MaxCopies || !claimed[id] {
			e = fmt.Errorf("orphan or unclassifiable snapshot/ordinary lease evidence")
			break
		}
	}
	return errorsWithRows(e, rows)
}
func errorsWithRows(e error, rows *sql.Rows) error {
	return errors.Join(e, rows.Err(), rows.Close())
}
