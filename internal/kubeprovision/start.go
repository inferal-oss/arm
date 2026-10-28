package kubeprovision

import (
	"context"
	"errors"
	"strconv"
)

const startMarkerScript = `set -eu
umask 077
key=$1; digest=$2; size=$3; expected_uid=$4; catalog=$5
test "${ARM_POD_UID:-}" = "$expected_uid"
root=/arm/private
test ! -e "$root/verified-start"
test "$(cat "$root/binary-receipt-$key")" = "$key $digest $size"
actual=$(sha256sum "$root/arm")
test "${actual%% *}" = "$digest"
test "$(wc -c < "$root/arm" | tr -d ' ')" = "$size"
proof="$root/catalog-proof-$key"
test ! -e "$proof"
"$root/arm" _kube-catalog-proof > "$proof"
test "$(wc -c < "$proof" | tr -d ' ')" = 86
test "$(cat "$proof")" = "ARM_CATALOG_VERIFIED $catalog"
rm "$proof"
printf '%s %s %s %s\n' "$key" "$digest" "$size" "$expected_uid" > "$root/start-stage-$key"
ln "$root/start-stage-$key" "$root/verified-start"
rm "$root/start-stage-$key"
`

const lookupStartScript = `set -eu
key=$1; digest=$2; size=$3; expected_uid=$4
test "${ARM_POD_UID:-}" = "$expected_uid"
test "$(cat /arm/private/verified-start)" = "$key $digest $size $expected_uid"
printf '%s\n' ARM_START_MARKER_VERIFIED
`

// LookupStartMarker proves only that the fixed bootstrap gate was published.
// It is not a running backend, mesh listener, SSH or resource-admission proof.
func (p Provider) LookupStartMarker(ctx context.Context, key string, transport ExecAPI) (Record, error) {
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
	args := []string{"/bin/sh", "-ec", lookupStartScript, "arm-start-lookup", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID}
	if err := p.execWithSource(ctx, key, transport, pod, args, nil, output, stderr); err != nil || output.String() != "ARM_START_MARKER_VERIFIED\n" || stderr.Len() != 0 || output.Overflow() || stderr.Overflow() {
		return unknownExecRecord(r, err)
	}
	return r, nil
}

// RequestStart durably fences the one fixed marker publication before exec.
// Subsequent calls inspect the original attempt without writing another gate.
func (p Provider) RequestStart(ctx context.Context, key string, transport ExecAPI) (Record, error) {
	if transport == nil {
		return Record{}, errors.New("Kubernetes exec transport required")
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
	if !ok || r.BinaryPhase != "binary_verified" {
		return r, ErrUnknown
	}
	if r.SourceCleanupUnknown {
		return r, ErrSourceOwnerUnavailable
	}
	r, err = p.LookupCatalog(ctx, key, transport)
	if err != nil {
		return r, err
	}
	if r.StartPhase != "" {
		return p.ReconcileStartMarker(ctx, key, transport)
	}
	r, err = p.updateSourceStage(ctx, key, r, func(next *Record) error { next.StartPhase = "start_attempted"; return nil })
	if err != nil {
		return r, err
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	args := []string{"/bin/sh", "-ec", startMarkerScript, "arm-start", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID, r.Plan.Review.CatalogSHA256}
	if err := p.execWithSource(ctx, key, transport, pod, args, nil, output, stderr); err != nil || output.Len() != 0 || stderr.Len() != 0 || output.Overflow() || stderr.Overflow() {
		return unknownExecRecord(r, err)
	}
	return p.ReconcileStartMarker(ctx, key, transport)
}

func (p Provider) ReconcileStartMarker(ctx context.Context, key string, transport ExecAPI) (Record, error) {
	r, err := p.LookupStartMarker(ctx, key, transport)
	if err != nil {
		return r, err
	}
	if r.StartPhase == "marker_verified" {
		return r, nil
	}
	if r.StartPhase != "start_attempted" {
		return r, ErrIdentity
	}
	return p.updateSourceStage(ctx, key, r, func(next *Record) error {
		if next.StartPhase != "start_attempted" {
			return ErrJournalConflict
		}
		next.StartPhase = "marker_verified"
		return nil
	})
}
