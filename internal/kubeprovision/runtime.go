package kubeprovision

import (
	"context"
	"errors"
	"regexp"
	"strconv"
)

const RuntimeProofArgument = "_kube-runtime-proof"

const runtimeProofScript = `set -eu
key=$1; digest=$2; size=$3; expected_uid=$4
test "${ARM_POD_UID:-}" = "$expected_uid"
root=/arm/private
test "$(cat "$root/verified-start")" = "$key $digest $size $expected_uid"
test "$(cat "$root/binary-receipt-$key")" = "$key $digest $size"
test "$(wc -c < "$root/arm" | tr -d ' ')" = "$size"
actual=$(sha256sum "$root/arm")
test "${actual%% *}" = "$digest"
exec "$root/arm" _kube-runtime-proof
`

var runtimeProofPattern = regexp.MustCompile(`^ARM_RUNTIME_VERIFIED ([a-f0-9]{32}) ([a-f0-9]{32}) ([0-9.eE+-]{1,32}) ([0-9]{1,20}) ([0-9]{1,20})\n$`)

// RuntimeProof is a fresh authenticated local-backend observation inside one
// exact Pod. It is not a mesh certificate, SSH host key or CPU grant.
type RuntimeProof struct {
	PodUID     string `json:"pod_uid"`
	Host       string `json:"host"`
	Generation string `json:"backend_generation"`
}

func (p Provider) LookupRuntime(ctx context.Context, key string, transport ExecAPI) (RuntimeProof, error) {
	if transport == nil {
		return RuntimeProof{}, errors.New("Kubernetes exec transport required")
	}
	r, err := p.LookupStartMarker(ctx, key, transport)
	if err != nil {
		return RuntimeProof{}, err
	}
	if r.StartPhase != "marker_verified" || r.BinaryPhase != "binary_verified" {
		return RuntimeProof{}, ErrUnknown
	}
	pod, err := p.ExactPod(ctx, key)
	if err != nil {
		return RuntimeProof{}, err
	}
	if pod.Phase != "Running" || pod.NodeName == "" {
		return RuntimeProof{}, errors.New("selected Pod is not running")
	}
	output, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	args := []string{"/bin/sh", "-ec", runtimeProofScript, "arm-runtime-proof", key, r.Plan.Review.BinarySHA256, strconv.FormatInt(r.Plan.Review.BinaryBytes, 10), pod.UID}
	if err := transport.Exec(ctx, pod, args, nil, output, stderr); err != nil || output.Overflow() || stderr.Overflow() || stderr.Len() != 0 {
		return RuntimeProof{}, ErrUnknown
	}
	parts := runtimeProofPattern.FindStringSubmatch(output.String())
	if len(parts) != 6 {
		return RuntimeProof{}, ErrUnknown
	}
	cpu, memory, disk, err := bootstrapBudgets(r.Plan.Review.Resources)
	if err != nil || parts[3] != cpu || parts[4] != memory || parts[5] != disk {
		return RuntimeProof{}, ErrIdentity
	}
	return RuntimeProof{PodUID: pod.UID, Host: parts[1], Generation: parts[2]}, nil
}
