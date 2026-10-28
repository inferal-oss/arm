package kubeprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ManagedSourceExec bridges the provider's fixed call map to an already
// admitted original source factory. It creates no temporary directory/file,
// selects no executable or endpoint, and grants no resource entitlement.
// Native callers supply their original Manager source's Files capability;
// this type is not constructed from a browser request or durable IDs.
type ManagedSourceExec struct {
	native *OwnedExec
	files  SourceFiles
}

func NewManagedSourceExec(plan Plan, files SourceFiles) (*ManagedSourceExec, error) {
	if files == nil {
		return nil, errors.New("admitted original source files required")
	}
	native, err := NewOwnedExec(plan)
	if err != nil {
		return nil, err
	}
	return &ManagedSourceExec{native: native, files: files}, nil
}

type managedSourcePayload struct {
	HeldSourceFile
	owner *ManagedSourceExec
	once  sync.Once
	err   error
}

func (p *managedSourcePayload) Close() error {
	p.once.Do(func() { p.err = p.HeldSourceFile.Close() })
	return p.err
}

// spool writes only the factory's one-shot original payload. The factory owns
// admission, the linked-empty allocation ACK and the cumulative byte bound
// before its returned file can be written. No generic source spool is used.
func (s *ManagedSourceExec) spool(ctx context.Context, source io.Reader, size int64, digest string) (HeldSourceFile, error) {
	if s == nil || s.native == nil || ctx == nil || ctx.Err() != nil || source == nil || size != s.native.Plan.Review.BinaryBytes || digest != s.native.Plan.Review.BinarySHA256 {
		return nil, ErrIdentity
	}
	raw, err := s.files.NewPayload(ctx)
	if err != nil || raw == nil {
		if raw != nil {
			if closeErr := raw.Close(); closeErr != nil {
				return nil, errors.Join(err, ErrHelperCleanupUnknown, closeErr)
			}
		}
		// No returned original means this adapter cannot prove which partial
		// construction resources the factory retained. Its caller must join
		// the original Manager source; do not clear provider custody early.
		return nil, errors.Join(err, ErrHelperCleanupUnknown, errors.New("original source payload unavailable"))
	}
	payload := &managedSourcePayload{HeldSourceFile: raw, owner: s}
	fail := func(cause error) (HeldSourceFile, error) {
		if closeErr := payload.Close(); closeErr != nil {
			return nil, errors.Join(cause, ErrHelperCleanupUnknown, closeErr)
		}
		return nil, cause
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(payload, h), &io.LimitedReader{R: &contextReader{ctx: ctx, reader: source}, N: size + 1})
	if err != nil {
		return fail(err)
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return fail(errors.New("reviewed Kubernetes binary bytes or digest changed"))
	}
	if _, err = payload.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return payload, nil
}

// Exec accepts only the provider's existing exact script/Pod map and hands
// the sealed call to the original factory. Native close/join evidence is
// checked before any helper output can become provider proof.
func (s *ManagedSourceExec) Exec(ctx context.Context, pod Pod, command []string, input io.Reader, output, stderr io.Writer) error {
	return s.exec(ctx, pod, command, input, output, stderr, nil)
}

func (s *ManagedSourceExec) exec(ctx context.Context, pod Pod, command []string, input io.Reader, output, stderr io.Writer, current func(context.Context) error) error {
	if s == nil || s.native == nil || ctx == nil || ctx.Err() != nil || output == nil || stderr == nil {
		return errors.New("live original managed source transport required")
	}
	call, err := s.native.NewSourceCallForExec(pod, command, input != nil)
	if err != nil {
		return err
	}
	if current != nil {
		if err := current(ctx); err != nil {
			return err
		}
		call.providerCurrent = current
	}
	var original HeldSourceFile
	if input != nil {
		payload, ok := input.(*managedSourcePayload)
		if !ok || payload.owner != s {
			return errors.New("same original managed payload required")
		}
		original = payload.HeldSourceFile
	}
	join, err := s.files.RunFixedExec(ctx, call, original)
	if !join.Matches(call) || !join.Closed() {
		return errors.Join(err, ErrHelperCleanupUnknown)
	}
	stdout, errout := join.Output()
	for _, stream := range []struct {
		text string
		to   io.Writer
	}{{stdout, output}, {errout, stderr}} {
		if n, writeErr := io.WriteString(stream.to, stream.text); writeErr != nil || n != len(stream.text) {
			return errors.Join(err, writeErr, fmt.Errorf("bounded managed helper output not delivered"))
		}
	}
	return err
}

// Only the provider builds this wrapper after durable ownership begins. It
// keeps preparation, target intent, fixed helpers and close in one original
// nonce/revision scope without weakening ordinary provider Update refusal.
type managedSourceOperation struct {
	source *ManagedSourceExec
	guard  *sourceExecGuard
}

func (o *managedSourceOperation) Exec(ctx context.Context, pod Pod, command []string, input io.Reader, output, stderr io.Writer) error {
	return o.source.exec(ctx, pod, command, input, output, stderr, o.guard.current)
}

// ShipBinaryWithManagedSource preserves the provider's original-key/source
// journal CAS protocol while using only admitted source files and sealed
// helper loans. A lost reply performs lookup; it never resends payload bytes.
func (p Provider) ShipBinaryWithManagedSource(ctx context.Context, key string, payload io.Reader, source *ManagedSourceExec) (Record, error) {
	if source == nil {
		return Record{}, errors.New("original managed source required")
	}
	return p.shipBinary(ctx, key, payload, source, source.spool)
}
