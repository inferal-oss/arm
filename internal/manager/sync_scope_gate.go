package manager

import (
	"context"

	"getarm.org/internal/syncscope"
)

// syncScopeGate remains the Manager-local spelling for the shared typed gate.
// Neither this wrapper nor the gate can mint a native owner or quota proof.
type syncScopeGate struct{ core *syncscope.Gate }

var errSyncScopeGateBusy = syncscope.ErrBusy

func newSyncScopeGate() *syncScopeGate {
	return &syncScopeGate{core: syncscope.NewGate()}
}

func (g *syncScopeGate) beginWrite(ctx context.Context) (*syncScopeWriter, error) {
	if g == nil {
		return nil, errSyncScopeGateBusy
	}
	w, err := g.core.BeginWrite(ctx)
	if err != nil {
		return nil, err
	}
	return &syncScopeWriter{core: w}, nil
}

func (g *syncScopeGate) tryVerify(ctx context.Context) (*syncScopeVerifier, error) {
	if g == nil {
		return nil, errSyncScopeGateBusy
	}
	v, err := g.core.TryVerify(ctx)
	if err != nil {
		return nil, err
	}
	return &syncScopeVerifier{core: v}, nil
}

type syncScopeWriter struct{ core *syncscope.Writer }

func (w *syncScopeWriter) release() {
	if w != nil {
		w.core.Release()
	}
}

func (w *syncScopeWriter) follows(v *syncScopeVerifier) bool {
	return w != nil && v != nil && w.core.Follows(v.core)
}

type syncScopeVerifier struct{ core *syncscope.Verifier }

func (v *syncScopeVerifier) release() {
	if v != nil {
		v.core.Release()
	}
}
