package kubeprovision

import (
	"context"
	"errors"
	"strconv"
)

// CatalogProofArgument is handled only by the exact ARM binary shipped to the
// Pod. It reports a verified embedded carrier manifest without starting ARM.
const CatalogProofArgument = "_kube-catalog-proof"

const catalogProofScript = `set -eu
key=$1; digest=$2; size=$3; expected_uid=$4
test "${ARM_POD_UID:-}" = "$expected_uid"
root=/arm/private
test -f "$root/arm"; test ! -L "$root/arm"
test "$(wc -c < "$root/arm" | tr -d ' ')" = "$size"
actual=$(sha256sum "$root/arm")
test "${actual%% *}" = "$digest"
test "$(cat "$root/binary-receipt-$key")" = "$key $digest $size"
exec "$root/arm" _kube-catalog-proof
`

// LookupCatalog proves the shipped binary embeds the exact reviewed carrier
// manifest and that its four payload streams verify. It does not start ARM,
// publish a marker or grant resource/mesh authority.
func (p Provider) LookupCatalog(ctx context.Context, key string, transport ExecAPI) (Record, error) {
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
	if !ok || r.Pod == nil || r.BinaryPhase != "binary_verified" {
		return r, ErrUnknown
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	args := []string{"/bin/sh", "-ec", catalogProofScript, "arm-catalog-proof", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID}
	want := "ARM_CATALOG_VERIFIED " + r.Plan.Review.CatalogSHA256 + "\n"
	if err := transport.Exec(ctx, pod, args, nil, output, stderr); err != nil || output.String() != want || stderr.Len() != 0 || output.Overflow() || stderr.Overflow() {
		return r, ErrUnknown
	}
	return r, nil
}
