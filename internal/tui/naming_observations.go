package tui

import (
	"context"
	"sync/atomic"

	"getarm.org/internal/model"
)

// The client issues requests concurrently. A delayed older healthy read must
// not hide a diagnostic accepted from a newer request in the same UI context.
var issuedNamingObservation atomic.Uint64

func callWithNamingObservation(ctx context.Context, session transport, operation string, params, result any) (*model.Error, error, uint64) {
	sequence := issuedNamingObservation.Add(1)
	diagnostic, err := callWithNaming(ctx, session, operation, params, result)
	return diagnostic, err, sequence
}
func (m *dashboardModel) observeNaming(sequence uint64, diagnostic *model.Error, healthy bool) {
	if sequence == 0 || sequence < m.namingObserved || diagnostic == nil && !healthy {
		return
	}
	m.namingObserved = sequence
	m.nameDiagnostic = diagnostic
}
