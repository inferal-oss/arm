package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/yrashk/arm/internal/local"
)

// Operator exposes only authenticated local inspection and authority-reducing
// settlement. Its private store has NO current certificate; no backing Store,
// approval, proof, issuance or refresh method is exposed by this facade.
type Operator struct{ store *Store }

func OpenOperator(ctx context.Context, db *sql.DB, a Authority, clock Clock) (*Operator, error) {
	if db == nil || !validAuthority(a) || a.UID != strconv.Itoa(os.Geteuid()) {
		return nil, fmt.Errorf("authenticated local workspace authority required")
	}
	if clock == nil {
		clock = realClock{start: time.Now()}
	}
	s := &Store{db: db, authority: a, clock: clock, boot: local.ID(), wallStart: clock.Wall(), elapsedStart: clock.Elapsed()}
	if _, e := s.Read(ctx); e != nil {
		return nil, e
	}
	return &Operator{s}, nil
}
func (o *Operator) Read(ctx context.Context) (Records, error) { return o.store.Read(ctx) }
func (o *Operator) Lookup(ctx context.Context, id, key string) (Operation, bool, error) {
	return o.store.Lookup(ctx, id, key)
}
func (o *Operator) Decline(ctx context.Context, id, key string, revision int64, review string) (Operation, error) {
	return o.store.Decide(ctx, id, key, revision, review, false)
}
func (o *Operator) Deny(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return o.store.Deny(ctx, id, q)
}
func (o *Operator) Revoke(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return o.store.Revoke(ctx, id, q)
}
func (o *Operator) Retire(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return o.store.Retire(ctx, id, q)
}
func (o *Operator) CancelAdmission(ctx context.Context, id, source, key string, revision int64) (Operation, error) {
	return o.store.CancelAdmission(ctx, id, source, key, revision)
}
func (o *Operator) CancelJoin(ctx context.Context, id, key string, revision int64) (Operation, error) {
	return o.store.CancelJoin(ctx, id, key, revision)
}
