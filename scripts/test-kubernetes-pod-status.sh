#!/usr/bin/env bash
# Offline acceptance cases for the actual read-only Pod status helper.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$here/kubernetes-pod-status.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

namespace=fixture-namespace
pod=fixture-pod
uid=fixture-pod-uid
container=builder
digest="sha256:$(printf 'a%.0s' {1..64})"
image="registry.invalid/fixture@$digest"
image_id="containerd://registry.invalid/fixture@$digest"
base="$(jq -cn --arg namespace "$namespace" --arg pod "$pod" --arg uid "$uid" \
  --arg container "$container" --arg image "$image" '{
    metadata: {namespace: $namespace, name: $pod, uid: $uid, labels: {acceptance: "fixture"}},
    spec: {containers: [{name: $container, image: $image}]}
  }')"
running="$(jq -c --arg image_id "$image_id" '.status={
  phase:"Running", conditions:[{type:"Ready",status:"False"}],
  containerStatuses:[{name:"builder",imageID:$image_id,state:{running:{}}}]
}' <<< "$base")"
succeeded="$(jq -c --arg image_id "$image_id" '.status={
  phase:"Succeeded", conditions:[{type:"Ready",status:"False"}],
  containerStatuses:[{name:"builder",imageID:$image_id,state:{terminated:{exitCode:0,reason:"Completed"}}}]
}' <<< "$base")"
failed="$(jq -c --arg image_id "$image_id" '.status={
  phase:"Failed", containerStatuses:[{name:"builder",imageID:$image_id,state:{terminated:{exitCode:1,reason:"Error"}}}]
}' <<< "$base")"
replacement="$(jq -c '.metadata.uid="replacement-uid"' <<< "$succeeded")"
wrong_image="$(jq -c '.status.containerStatuses[0].imageID="containerd://sha256:bbbb"' <<< "$succeeded")"

fixture_file= index_file=
read_fixture() {
  local index item
  index="$(cat "$index_file")"
  item="$(sed -n "${index}p" "$fixture_file")"
  printf '%s\n' "$((index + 1))" > "$index_file"
  [[ -n "$item" && "$item" != READ_ERROR ]] || return 42
  printf '%s\n' "$item"
}
validate_fixture() {
  jq -e '.metadata.labels.acceptance == "fixture"' <<< "$1" >/dev/null
}

run_case() {
  local name=$1 target=$2 budget=$3 expected_result=$4 expected_stage=$5
  local expected_uid=$6 expected_phase=$7 expected_exit=$8
  shift 8
  fixture_file="$scratch/$name.jsonl"
  index_file="$scratch/$name.index"
  local status_path="$scratch/$name.status.json" failure_path="$scratch/$name.failure.json"
  printf '%s\n' "$@" > "$fixture_file"
  printf '1\n' > "$index_file"
  local result=success
  arm_kube_poll_pod_status "$target" "$budget" "$namespace" "$pod" "$uid" \
    "$container" "$image" "$status_path" "$failure_path" read_fixture validate_fixture || result=failure
  [[ "$result" == "$expected_result" && "$ARM_KUBE_POLL_STAGE" == "$expected_stage" ]] || {
    printf '%s: got %s/%s; want %s/%s\n' "$name" "$result" "$ARM_KUBE_POLL_STAGE" \
      "$expected_result" "$expected_stage" >&2
    exit 1
  }
  jq -e --arg uid "$expected_uid" --arg phase "$expected_phase" --arg exit "$expected_exit" '
    .observed_pod_uid == $uid and .phase == $phase and
    ((.terminated_exit_code | if . == null then "null" else tostring end) == $exit)
  ' "$status_path" >/dev/null
  if [[ "$result" == failure ]]; then
    jq -e --arg stage "$expected_stage" --arg uid "$expected_uid" '
      .stage == $stage and .last_observed.observed_pod_uid == $uid
    ' "$failure_path" >/dev/null
  else
    [[ ! -e "$failure_path" ]]
  fi
  printf '%s: %s (%s)\n' "$name" "$result" "$ARM_KUBE_POLL_STAGE"
}

run_case running_to_succeeded terminal 3 success success "$uid" Succeeded 0 "$running" "$succeeded"
run_case completed_before_ready ready 2 success success "$uid" Succeeded 0 "$succeeded"
run_case failed_pod terminal 3 failure pod_failed "$uid" Failed 1 "$failed"
run_case replacement terminal 3 failure identity_mismatch replacement-uid Succeeded 0 "$replacement"
run_case read_failure terminal 3 failure read_failed "$uid" Running null "$running" READ_ERROR
run_case timeout terminal 1 failure timeout "$uid" Running null "$running"
run_case wrong_image terminal 3 failure identity_mismatch "$uid" Succeeded 0 "$wrong_image"
fixture_file="$scratch/first_read_failure.jsonl"
index_file="$scratch/first_read_failure.index"
printf 'READ_ERROR\n' > "$fixture_file"
printf '1\n' > "$index_file"
if arm_kube_poll_pod_status terminal 2 "$namespace" "$pod" "$uid" "$container" "$image" \
    "$scratch/first_read_failure.status.json" "$scratch/first_read_failure.failure.json" \
    read_fixture validate_fixture; then
  echo 'first_read_failure unexpectedly passed' >&2
  exit 1
fi
[[ "$ARM_KUBE_POLL_STAGE" == read_failed && ! -e "$scratch/first_read_failure.status.json" ]]
jq -e '.stage == "read_failed" and .last_observed == null' \
  "$scratch/first_read_failure.failure.json" >/dev/null
printf 'first_read_failure: failure (read_failed; no observed status)\n'
printf 'All fixtures used only the injected reader; no Kubernetes calls.\n'
