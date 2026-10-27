// Package providerfixture is a deliberately opt-in external provider test
// executable's durable state machine. It is not ARM admission or a sandbox.
package providerfixture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	p "getarm.org/internal/providerproto"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const MaxObjects = 64
const MaxOperations = 256

type Config struct {
	Capacity     string `json:"capacity"`
	ReadyDelayMS string `json:"ready_delay_ms"`
	DenyDelete   bool   `json:"deny_delete"`
}
type specification struct {
	Name string `json:"name"`
}
type object struct {
	Identity            p.Identity
	Original            []byte
	Name                string
	Revision            uint64
	State               string
	ReadyAt             time.Time
	ExpiresAt           time.Time
	Dependencies        []p.Identity
	Receipt             p.Digest
	CreationReadyDigest p.Digest
}
type Store struct{ db *sql.DB }

// Open requires an existing private disposable directory. Its parents and
// ownership are the test operator's responsibility, not a providerexec proof.
func Open(home string) (*Store, error) {
	info, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("fixture needs an existing mode-0700 directory")
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "external.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=DELETE", "PRAGMA synchronous=FULL",
		`CREATE TABLE IF NOT EXISTS operations (authority TEXT, registration TEXT, key TEXT, original BLOB NOT NULL, response BLOB NOT NULL, PRIMARY KEY(authority,registration,key))`,
		`CREATE TABLE IF NOT EXISTS objects (id TEXT PRIMARY KEY, body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS selection (id INTEGER PRIMARY KEY CHECK(id=1), registration BLOB NOT NULL, authority TEXT NOT NULL)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func decodeData(d p.Data, v any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(d.Canonical(), &fields); err != nil {
		return err
	}
	var expected []string
	switch v.(type) {
	case *Config:
		expected = []string{"capacity", "ready_delay_ms", "deny_delete"}
	case *specification:
		expected = []string{"name"}
	default:
		return fmt.Errorf("unsupported fixture data type")
	}
	if len(fields) != len(expected) {
		return fmt.Errorf("fixture data fields differ")
	}
	for _, key := range expected {
		if raw, ok := fields[key]; !ok || bytes.Equal(raw, []byte("null")) {
			return fmt.Errorf("fixture data field absent")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(d.Canonical()))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
func configuration(q p.Request) (Config, time.Duration, error) {
	var c Config
	if err := decodeData(q.Configuration, &c); err != nil {
		return c, 0, err
	}
	capacity, err := strconv.Atoi(c.Capacity)
	if err != nil || capacity < 1 || capacity > MaxObjects || strconv.Itoa(capacity) != c.Capacity {
		return c, 0, fmt.Errorf("invalid fixture capacity")
	}
	delay, err := strconv.Atoi(c.ReadyDelayMS)
	if err != nil || delay < 0 || delay > 60000 || strconv.Itoa(delay) != c.ReadyDelayMS {
		return c, 0, fmt.Errorf("invalid fixture delay")
	}
	if q.Registration.ResourceType != "fixture-object" || q.Registration.ImplementationVersion != "fixture-1" {
		return c, 0, fmt.Errorf("unsupported fixture registration")
	}
	return c, time.Duration(delay) * time.Millisecond, nil
}
func reply(q p.Request) p.Response {
	return p.Response{Protocol: p.Protocol, Version: p.Version, Kind: "response", RequestID: q.RequestID, Registration: q.Registration, Method: q.Method, OperationKey: q.Context.OperationKey, IntentSHA256: q.Context.IntentSHA256}
}
func failure(q p.Request, code string) p.Response {
	r := reply(q)
	wireCode := code
	if code == "budget_exceeded" || code == "permission_denied" {
		wireCode = "denied"
	}
	r.Error = &p.ProtocolError{Code: wireCode, Message: p.Text("fixture " + code)}
	return r
}
func data(v any) (p.Data, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return p.Data{}, err
	}
	return p.ParseData(raw)
}
func ReceiptDigest(r p.OperationReceipt) (p.Digest, error) { d, err := data(r); return d.SHA256(), err }
func receipt(q p.Request, o object, prior *p.Digest) (p.OperationReceipt, error) {
	ev, err := data(map[string]string{"object_id": string(o.Identity.ID), "incarnation": string(o.Identity.Incarnation), "expires_at": o.ExpiresAt.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return p.OperationReceipt{}, err
	}
	original, err := p.DecodeRequest(o.Original)
	if err != nil {
		return p.OperationReceipt{}, err
	}
	spec := original.Request().Params.(p.CreateParams).SpecificationSHA256
	return p.OperationReceipt{Kind: "operation", ProviderRequestID: p.ID(q.Context.OperationKey), State: o.State, ExternalIdentity: &o.Identity, Revision: p.Uint(strconv.FormatUint(o.Revision, 10)), SpecificationSHA256: spec, PriorReceiptSHA256: prior, Evidence: p.Evidence{Format: "fixture/v1", Data: ev, SHA256: ev.SHA256()}, Metadata: p.NullData()}, nil
}
func loadObjects(ctx context.Context, conn *sql.Conn) ([]object, error) {
	rows, err := conn.QueryContext(ctx, "SELECT body FROM objects LIMIT ?", MaxObjects+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []object
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var o object
		if err = json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}
	if len(objects) > MaxObjects {
		return nil, fmt.Errorf("fixture object bound exceeded")
	}
	return objects, rows.Err()
}
func saveObject(ctx context.Context, conn *sql.Conn, o object, insert bool) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	var result sql.Result
	if insert {
		result, err = conn.ExecContext(ctx, "INSERT INTO objects(id,body) VALUES(?,?)", string(o.Identity.ID), raw)
	} else {
		result, err = conn.ExecContext(ctx, "UPDATE objects SET body=? WHERE id=?", raw, string(o.Identity.ID))
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("fixture object write did not match one identity")
	}
	return nil
}

// Handle serializes a complete external transaction, including immutable key
// deduplication and the positive deletion tombstone. Fault hooks are test-only
// barriers; a lost transaction/commit response is never translated to no effect.
func (s *Store) Handle(ctx context.Context, q p.Request, hook func(string)) (p.Response, error) {
	// Even internal callers must supply a structurally validated wire value.
	raw, err := p.EncodeRequest(q)
	if err != nil {
		return p.Response{}, err
	}
	decoded, err := p.DecodeRequest(raw)
	if err != nil {
		return p.Response{}, err
	}
	q = decoded.Request()
	c, delay, err := configuration(q)
	if err != nil {
		return failure(q, "invalid_configuration"), nil
	}
	if q.Configuration.SHA256() != q.Registration.ConfigSHA256 {
		return failure(q, "conflict"), nil
	}
	if q.Method != p.Lookup && q.Method != p.Reconcile {
		if _, err = p.OriginalFromTrusted(q); err != nil {
			return failure(q, "conflict"), nil
		}
	}
	r := reply(q)
	switch q.Method {
	case p.Describe:
		r.Result = p.Descriptor{Kind: "descriptor", ImplementationID: "arm-test-fixture", ImplementationVersion: "fixture-1", ProtocolVersions: []int{1}, ResourceTypes: []p.Name{"fixture-object"}, Methods: []p.Method{p.Describe, p.Validate, p.Plan, p.Create, p.Lookup, p.Renew, p.Release, p.Reconcile}, DurableCreateDedup: true, DurableTerminalTombstones: true, KeyLookup: true, ImmutableIdentity: true, ReadOnlyReconcile: true, Metadata: p.NullData()}
		return r, nil
	case p.Validate:
		r.Result = p.Validated{Kind: "validated", ConfigurationSHA256: q.Configuration.SHA256()}
		return r, nil
	case p.Plan:
		v := q.Params.(p.PlanParams)
		var spec specification
		if e := decodeData(v.Specification, &spec); e != nil || spec.Name == "" || len(spec.Name) > 128 {
			return failure(q, "invalid_specification"), nil
		}
		readiness := "delayed"
		if delay == 0 {
			readiness = "immediate"
		}
		r.Result = p.Planned{Kind: "planned", SpecificationSHA256: v.Specification.SHA256(), EstimatedBudgets: []p.Budget{{Unit: "objects", Limit: "1"}}, Readiness: readiness, Metadata: p.NullData()}
		return r, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return r, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return r, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	// One fixture home has one immutable external selection. A new key must
	// not silently raise capacity or change account/code/config generation.
	registrationRaw, err := json.Marshal(q.Registration)
	if err != nil {
		return r, err
	}
	var selected []byte
	var authority string
	err = conn.QueryRowContext(ctx, "SELECT registration,authority FROM selection WHERE id=1").Scan(&selected, &authority)
	if err == nil {
		if !bytes.Equal(selected, registrationRaw) || authority != string(q.Context.Authority) {
			return failure(q, "conflict"), nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	} else if q.Method != p.Lookup && q.Method != p.Reconcile {
		if _, err = conn.ExecContext(ctx, "INSERT INTO selection VALUES(1,?,?)", registrationRaw, string(q.Context.Authority)); err != nil {
			return r, err
		}
	}
	var originalRaw, responseRaw []byte
	err = conn.QueryRowContext(ctx, "SELECT original,response FROM operations WHERE authority=? AND registration=? AND key=?", string(q.Context.Authority), string(q.Registration.ID), string(q.Context.OperationKey)).Scan(&originalRaw, &responseRaw)
	if err == nil {
		decoded, e := p.DecodeRequest(originalRaw)
		if e != nil {
			return r, e
		}
		original := decoded.Request()
		reg, e := p.RegistrationFromTrusted(original.Registration, original.Configuration)
		if e != nil {
			return r, e
		}
		saved, e := p.OriginalFromTrusted(original)
		if e != nil {
			return r, e
		}
		stored, e := p.DecodeResponse(responseRaw)
		if e != nil {
			return r, e
		}
		var target *p.ExpectedTarget
		switch v := original.Params.(type) {
		case p.CreateParams:
			if query, ok := q.Params.(p.QueryParams); ok && query.ExternalIdentity != nil {
				rec, ok := stored.Response().Result.(p.OperationReceipt)
				if ok && rec.ExternalIdentity != nil {
					digest, e := ReceiptDigest(rec)
					if e != nil {
						return r, e
					}
					// Both the immutable initial receipt and current readiness
					// observation are valid caller-known lookup anchors.
					if query.PriorReceiptSHA256 != nil && digest != *query.PriorReceiptSHA256 {
						objects, e := loadObjects(ctx, conn)
						if e != nil {
							return r, e
						}
						for _, o := range objects {
							if o.Identity == *rec.ExternalIdentity && o.CreationReadyDigest != "" && o.CreationReadyDigest == *query.PriorReceiptSHA256 {
								digest = o.CreationReadyDigest
							}
						}
					}
					t, e := p.TargetFromTrusted(*rec.ExternalIdentity, digest, rec.Revision, rec.SpecificationSHA256)
					if e != nil {
						return r, e
					}
					target = &t
				}
			}
		case p.RenewParams:
			rec, ok := stored.Response().Result.(p.OperationReceipt)
			if !ok {
				break
			}
			t, e := p.TargetFromTrusted(v.ExternalIdentity, v.PriorReceiptSHA256, v.ExpectedRevision, rec.SpecificationSHA256)
			if e != nil {
				return r, e
			}
			target = &t
		case p.ReleaseParams:
			rec, ok := stored.Response().Result.(p.OperationReceipt)
			if !ok {
				break
			}
			t, e := p.TargetFromTrusted(v.ExternalIdentity, v.PriorReceiptSHA256, v.ExpectedRevision, rec.SpecificationSHA256)
			if e != nil {
				return r, e
			}
			target = &t
		}
		// For durable error retries, compare original bytes directly after replacing
		// only the transport correlation ID. Queries must bind original attribution.
		if target == nil && (original.Method == p.Renew || original.Method == p.Release) {
			expected := original
			expected.RequestID = q.RequestID
			if q.Method == p.Lookup || q.Method == p.Reconcile {
				expected.Method = q.Method
				qp := p.QueryParams{TargetMethod: original.Method}
				switch v := original.Params.(type) {
				case p.RenewParams:
					i, d := v.ExternalIdentity, v.PriorReceiptSHA256
					qp.ExternalIdentity = &i
					qp.PriorReceiptSHA256 = &d
				case p.ReleaseParams:
					i, d := v.ExternalIdentity, v.PriorReceiptSHA256
					qp.ExternalIdentity = &i
					qp.PriorReceiptSHA256 = &d
				}
				expected.Params = qp
			}
			a, e := p.EncodeRequest(expected)
			if e != nil {
				return r, e
			}
			b, e := p.EncodeRequest(q)
			if e != nil {
				return r, e
			}
			if string(a) != string(b) {
				return failure(q, "conflict"), nil
			}
		} else {
			raw, e := p.EncodeRequest(q)
			if e != nil {
				return r, e
			}
			dec, e := p.DecodeRequest(raw)
			if e != nil {
				return r, e
			}
			if _, e = p.MatchRequest(dec, reg, saved, target); e != nil {
				return failure(q, "conflict"), nil
			}
		}
		r = stored.Response()
		r.RequestID = q.RequestID
		r.Method = q.Method
		// Observation may advance scheduled readiness, but never creates/deletes.
		// A superseded receipt is unknown, never fresh readiness.
		if (q.Method == p.Lookup || q.Method == p.Reconcile) && original.Method != p.Release && r.Error == nil {
			rec := r.Result.(p.OperationReceipt)
			objects, e := loadObjects(ctx, conn)
			if e != nil {
				return r, e
			}
			found := false
			for _, o := range objects {
				if rec.ExternalIdentity != nil && o.Identity == *rec.ExternalIdentity {
					found = true
					if p.Uint(strconv.FormatUint(o.Revision, 10)) != rec.Revision || o.State == "released" || !time.Now().Before(o.ExpiresAt) {
						rec.State = "unknown"
						rec.Revision = p.Uint(strconv.FormatUint(o.Revision, 10))
						r.Result = rec
						return r, nil
					}
					if o.State == "pending" && !time.Now().Before(o.ReadyAt) {
						o.State = "ready"
						observed, e := receipt(original, o, rec.PriorReceiptSHA256)
						if e != nil {
							return r, e
						}
						o.Receipt, e = ReceiptDigest(observed)
						if e != nil {
							return r, e
						}
						if original.Method == p.Create {
							o.CreationReadyDigest = o.Receipt
						}
						if e = saveObject(ctx, conn, o, false); e != nil {
							return r, e
						}
						if _, e = conn.ExecContext(ctx, "COMMIT"); e != nil {
							return r, e
						}
					}
					observed, e := receipt(original, o, rec.PriorReceiptSHA256)
					if e != nil {
						return r, e
					}
					r.Result = observed
				}
			}
			if !found {
				// A saved operation receipt is historical; missing external
				// state cannot establish fresh readiness or reclamation.
				rec.State = "unknown"
				r.Result = rec
			}
		}
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if q.Method == p.Lookup || q.Method == p.Reconcile {
		return failure(q, "not_found"), nil
	}
	var count int
	if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM operations").Scan(&count); err != nil {
		return r, err
	}
	if count >= MaxOperations {
		return failure(q, "budget_exceeded"), nil
	}
	objects, err := loadObjects(ctx, conn)
	if err != nil {
		return r, err
	}
	now := time.Now().UTC()
	var changed *object
	switch v := q.Params.(type) {
	case p.CreateParams:
		var spec specification
		if e := decodeData(v.Specification, &spec); e != nil || spec.Name == "" || len(spec.Name) > 128 {
			r = failure(q, "invalid_specification")
			break
		}
		capacity, _ := strconv.Atoi(c.Capacity)
		live := 0
		for _, o := range objects {
			if o.State != "released" {
				live++
				if o.Name == spec.Name {
					r = failure(q, "conflict")
				}
			}
		}
		if r.Error != nil {
			break
		}
		if len(objects) >= MaxObjects || live >= capacity {
			r = failure(q, "budget_exceeded")
			break
		}
		if len(v.Budgets) != 1 || v.Budgets[0] != (p.Budget{Unit: "objects", Limit: "1"}) {
			r = failure(q, "budget_exceeded")
			break
		}
		expiry, e := time.Parse(time.RFC3339Nano, string(v.ExpiresAt))
		if e != nil || !expiry.After(now.Add(delay)) {
			r = failure(q, "conflict")
			break
		}
		for _, dep := range v.Dependencies {
			found := false
			for _, o := range objects {
				if o.Identity == dep && o.State != "released" && now.Before(o.ExpiresAt) && !now.Before(o.ReadyAt) {
					born, e := p.DecodeRequest(o.Original)
					if e != nil {
						return r, e
					}
					parent := born.Request()
					if parent.Registration == q.Registration && parent.Context.Authority == q.Context.Authority && parent.Context.OwnerUID == q.Context.OwnerUID && parent.Context.Scope == q.Context.Scope {
						found = true
					}
				}
			}
			if !found {
				r = failure(q, "conflict")
				break
			}
		}
		if r.Error != nil {
			break
		}
		raw, e := p.EncodeRequest(q)
		if e != nil {
			return r, e
		}
		o := object{Identity: p.Identity{ExternalScope: q.Registration.ExternalScope, ID: p.ID(uuid.NewString()), Incarnation: p.ID(uuid.NewString())}, Original: raw, Name: spec.Name, Revision: 1, State: "pending", ReadyAt: now.Add(delay), ExpiresAt: expiry, Dependencies: v.Dependencies}
		// Readiness is fixed by this transaction. Delayed objects remain pending;
		// later lookup observes their scheduled readiness without another create.
		if delay == 0 {
			o.State = "ready"
		}
		rec, e := receipt(q, o, nil)
		if e != nil {
			return r, e
		}
		o.Receipt, e = ReceiptDigest(rec)
		if e != nil {
			return r, e
		}
		if o.State == "ready" {
			o.CreationReadyDigest = o.Receipt
		}
		r.Result = rec
		changed = &o
	case p.RenewParams:
		r, changed, err = mutate(q, objects, now, c, v.ExternalIdentity, v.PriorReceiptSHA256, v.ExpectedRevision, false, string(v.ExpiresAt))
	case p.ReleaseParams:
		if v.Action != "delete" {
			r = failure(q, "unsupported")
			break
		}
		r, changed, err = mutate(q, objects, now, c, v.ExternalIdentity, v.PriorReceiptSHA256, v.ExpectedRevision, true, "")
	default:
		r = failure(q, "unsupported")
	}
	if err != nil {
		return r, err
	}
	if changed != nil {
		if err = saveObject(ctx, conn, *changed, q.Method == p.Create); err != nil {
			return r, err
		}
	}
	originalRaw, err = p.EncodeRequest(q)
	if err != nil {
		return r, err
	}
	responseRaw, err = p.EncodeResponse(r)
	if err != nil {
		return r, err
	}
	if _, err = conn.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?,?)", string(q.Context.Authority), string(q.Registration.ID), string(q.Context.OperationKey), originalRaw, responseRaw); err != nil {
		return r, err
	}
	if hook != nil {
		hook("before-commit")
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return r, err
	}
	if hook != nil {
		hook("after-commit")
	}
	return r, nil
}
func mutate(q p.Request, objects []object, now time.Time, c Config, id p.Identity, prior p.Digest, revision p.Uint, release bool, expiry string) (p.Response, *object, error) {
	r := reply(q)
	for _, o := range objects {
		if o.Identity != id {
			continue
		}
		decoded, err := p.DecodeRequest(o.Original)
		if err != nil {
			return r, nil, err
		}
		born := decoded.Request()
		if born.Registration != q.Registration || born.Context.Authority != q.Context.Authority || born.Context.OwnerUID != q.Context.OwnerUID || born.Context.Scope != q.Context.Scope || o.Receipt != prior || p.Uint(strconv.FormatUint(o.Revision, 10)) != revision || o.State == "released" {
			return failure(q, "conflict"), nil, nil
		}
		if release {
			if c.DenyDelete {
				return failure(q, "permission_denied"), nil, nil
			}
			for _, dependent := range objects {
				if dependent.State != "released" {
					for _, dep := range dependent.Dependencies {
						if dep == id {
							return failure(q, "conflict"), nil, nil
						}
					}
				}
			}
			o.State = "released"
		} else {
			end, err := time.Parse(time.RFC3339Nano, expiry)
			if err != nil || !now.Before(o.ExpiresAt) || !end.After(o.ExpiresAt) {
				return failure(q, "conflict"), nil, nil
			}
			o.ExpiresAt = end
			if !now.Before(o.ReadyAt) {
				o.State = "ready"
			}
		}
		o.Revision++
		rec, err := receipt(q, o, &prior)
		if err != nil {
			return r, nil, err
		}
		o.Receipt, err = ReceiptDigest(rec)
		if err != nil {
			return r, nil, err
		}
		r.Result = rec
		return r, &o, nil
	}
	// Absence of an immutable identity is never a successful deletion receipt.
	return failure(q, "not_found"), nil, nil
}
