package kubeprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

// ExecAPI is the client-go streaming adapter used for one fixed payload
// transfer. Arbitrary shell commands are not accepted by the provider API.
type ExecAPI interface {
	Exec(context.Context, Pod, []string, io.Reader, io.Writer, io.Writer) error
}

// spoolBinary separates reading the caller's payload from the ambiguous
// remote exec attempt. The unlinked 0600 file is owned until Exec has sealed
// its input; an interrupted exec never retains the caller's reader or hash.
func spoolBinary(ctx context.Context, source io.Reader, size int64, expected string) (*os.File, error) {
	file, err := os.CreateTemp("", "arm-kube-payload-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		file.Close()
		return nil, err
	}
	failed := func(err error) (*os.File, error) { file.Close(); return nil, err }
	h := sha256.New()
	limited := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: source}, N: size + 1}
	n, err := io.Copy(io.MultiWriter(file, h), limited)
	if err != nil {
		return failed(err)
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != expected {
		return failed(errors.New("reviewed Kubernetes binary bytes or digest changed"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return failed(err)
	}
	return file, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

const shipScript = `set -eu
key=$1; digest=$2; size=$3; expected_uid=$4
test "${ARM_POD_UID:-}" = "$expected_uid"
umask 077
root=/arm/private
test -d "$root"; test ! -L "$root"
test ! -e "$root/arm"; test ! -e "$root/binary-receipt-$key"
stage="$root/stage-$key"
mkdir "$stage"
cat > "$stage/arm"
test "$(wc -c < "$stage/arm" | tr -d ' ')" = "$size"
actual=$(sha256sum "$stage/arm")
test "${actual%% *}" = "$digest"
chmod 700 "$stage/arm"
ln "$stage/arm" "$root/arm"
printf '%s %s %s\n' "$key" "$digest" "$size" > "$stage/receipt"
ln "$stage/receipt" "$root/binary-receipt-$key"
rm "$stage/arm" "$stage/receipt"
rmdir "$stage"
`

const lookupBinaryScript = `set -eu
key=$1; digest=$2; size=$3; expected_uid=$4
test "${ARM_POD_UID:-}" = "$expected_uid"
root=/arm/private
test -f "$root/arm"; test ! -L "$root/arm"
test "$(wc -c < "$root/arm" | tr -d ' ')" = "$size"
actual=$(sha256sum "$root/arm")
test "${actual%% *}" = "$digest"
test "$(cat "$root/binary-receipt-$key")" = "$key $digest $size"
printf '%s\n' ARM_BINARY_VERIFIED
`

func (p Provider) LookupBinary(ctx context.Context, key string, transport ExecAPI) (Record, error) {
	if transport == nil {
		return Record{}, errors.New("Kubernetes exec transport required")
	}
	pod, err := p.ExactPod(ctx, key)
	if err != nil {
		return Record{}, err
	}
	r, ok, err := p.Journal.Read(ctx, key)
	if err != nil {
		return r, err
	}
	if !ok || r.Pod == nil {
		return r, ErrUnknown
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	args := []string{"/bin/sh", "-ec", lookupBinaryScript, "arm-lookup", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID}
	if err := p.execWithSource(ctx, key, transport, pod, args, nil, output, stderr); err != nil || output.String() != "ARM_BINARY_VERIFIED\n" || stderr.Len() != 0 || output.Overflow() || stderr.Overflow() {
		return unknownExecRecord(r, err)
	}
	return r, nil
}

// ShipBinary makes one bounded exec attempt after durably marking the source
// intent. A lost stream or ACK remains unknown; calls with the original key do
// read-only lookup and never send payload bytes again.
func (p Provider) ShipBinary(ctx context.Context, key string, payload io.Reader, transport ExecAPI) (Record, error) {
	return p.shipBinary(ctx, key, payload, transport, func(ctx context.Context, source io.Reader, size int64, digest string) (HeldSourceFile, error) {
		return spoolBinary(ctx, source, size, digest)
	})
}

func (p Provider) shipBinary(ctx context.Context, key string, payload io.Reader, transport ExecAPI, spool func(context.Context, io.Reader, int64, string) (HeldSourceFile, error)) (record Record, resultErr error) {
	if payload == nil || transport == nil {
		return Record{}, errors.New("binary and Kubernetes exec transport required")
	}
	pod, err := p.ExactPod(ctx, key)
	if err != nil {
		return Record{}, err
	}
	if pod.Phase != "Running" || pod.NodeName == "" {
		return Record{}, errors.New("selected Pod is not running")
	}
	r, ok, err := p.Journal.Read(ctx, key)
	if err != nil {
		return r, err
	}
	if !ok || r.Phase != PhaseCreated || r.Pod == nil {
		return r, ErrUnknown
	}
	if r.SourceCleanupUnknown {
		return r, ErrSourceOwnerUnavailable
	}
	if r.BinaryPhase != "" {
		return p.ReconcileBinary(ctx, key, transport)
	}
	var preparation *sourceExecGuard
	if managed, ok := transport.(*ManagedSourceExec); ok {
		if managed.native == nil || managed.native.Plan.SHA256 != r.Plan.SHA256 {
			return r, ErrIdentity
		}
		journal, ok := p.Journal.(sourcePreparationJournal)
		if !ok {
			return r, errors.New("durable original Kubernetes source ownership required")
		}
		preparation, err = journal.beginSourcePreparation(ctx, key, r.Plan.SHA256, pod.UID, r.Revision)
		if err != nil {
			return r, err
		}
		transport = &managedSourceOperation{source: managed, guard: preparation}
		defer func() {
			panicked := recover()
			unknown := panicked != nil || errors.Is(resultErr, ErrHelperCleanupUnknown)
			if closeErr := preparation.finish(unknown); closeErr != nil {
				resultErr = errors.Join(resultErr, ErrHelperCleanupUnknown, closeErr)
				record.SourceCleanupUnknown = true
			} else {
				record.SourceCleanupUnknown = false
			}
			if panicked != nil {
				panic(panicked)
			}
		}()
	}
	update := func(prior Record, change func(*Record) error) (Record, error) {
		if preparation != nil {
			return preparation.advance(ctx, p.Journal, prior, change)
		}
		return p.updateSourceStage(ctx, key, prior, change)
	}
	spooled, err := spool(ctx, payload, r.Plan.Review.BinaryBytes, r.Plan.Review.BinarySHA256)
	if err != nil {
		return r, err
	}
	var closeOnce sync.Once
	var closeErr error
	closeSpool := func() error {
		closeOnce.Do(func() { closeErr = spooled.Close() })
		return closeErr
	}
	defer func() {
		if err := closeSpool(); err != nil {
			resultErr = errors.Join(resultErr, ErrHelperCleanupUnknown, err)
		}
	}()
	if fresh, err := p.ExactPod(ctx, key); err != nil || fresh.UID != pod.UID {
		if err != nil {
			return r, err
		}
		return r, ErrIdentity
	}
	r, err = update(r, func(next *Record) error { next.BinaryPhase = "ship_attempted"; return nil })
	if err != nil {
		return r, err
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	args := []string{"/bin/sh", "-ec", shipScript, "arm-ship", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID}
	streamErr := p.execWithSourceCleanup(ctx, key, transport, pod, args, spooled, output, stderr, closeSpool)
	if streamErr != nil || output.Len() != 0 || stderr.Len() != 0 || output.Overflow() || stderr.Overflow() {
		return unknownExecRecord(r, streamErr)
	}
	if _, err := p.LookupBinary(ctx, key, transport); err != nil {
		return unknownExecRecord(r, err)
	}
	return update(r, func(next *Record) error {
		if next.BinaryPhase != "ship_attempted" {
			return ErrJournalConflict
		}
		next.BinaryPhase = "binary_verified"
		return nil
	})
}

// ReconcileBinary settles only positive exact-key read-only destination
// evidence. A failed lookup preserves the attempted transfer as unknown.
func (p Provider) ReconcileBinary(ctx context.Context, key string, transport ExecAPI) (Record, error) {
	r, err := p.LookupBinary(ctx, key, transport)
	if err != nil {
		return r, err
	}
	if r.BinaryPhase == "binary_verified" {
		return r, nil
	}
	if r.BinaryPhase != "ship_attempted" {
		return r, ErrIdentity
	}
	return p.updateSourceStage(ctx, key, r, func(next *Record) error {
		if next.BinaryPhase != "ship_attempted" {
			return ErrJournalConflict
		}
		next.BinaryPhase = "binary_verified"
		return nil
	})
}

// BinaryReceiptText is the exact file content the fixed Pod script publishes.
func BinaryReceiptText(key, digest string, size int64) (string, error) {
	if !digestPattern.MatchString(key) || !digestPattern.MatchString(digest) || size < 1 || size > MaxBinaryBytes {
		return "", fmt.Errorf("invalid bounded binary receipt")
	}
	return strings.Join([]string{key, digest, strconv.FormatInt(size, 10)}, " ") + "\n", nil
}
