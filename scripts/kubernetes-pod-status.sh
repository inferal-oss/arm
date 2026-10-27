#!/usr/bin/env bash
# Source this file from an acceptance wrapper. The reader and validator are
# caller-supplied Bash functions; this helper never creates or deletes objects.

arm_kube_status_receipt() {
  local path=$1 json=$2 expected_uid=$3 tmp
  jq -e -s 'length == 1 and (.[0] | type == "object")' <<< "$json" >/dev/null || return 1
  tmp="$(mktemp "${path}.tmp.XXXXXX")" || return 1
  if ! jq --arg expected_uid "$expected_uid" '{
    expected_pod_uid: $expected_uid,
    observed_pod_uid: .metadata.uid,
    phase: .status.phase,
    terminated_exit_code: (.status.containerStatuses[0].state.terminated.exitCode // null),
    terminated_reason: (.status.containerStatuses[0].state.terminated.reason // null),
    image_id: (.status.containerStatuses[0].imageID // null)
  }' <<< "$json" > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  mv -f "$tmp" "$path"
}

arm_kube_failure_receipt() {
  local path=$1 target=$2 stage=$3 expected_uid=$4 status_path=$5 tmp
  tmp="$(mktemp "${path}.tmp.XXXXXX")" || return 1
  if [[ -f "$status_path" ]]; then
    jq -n --arg target "$target" --arg stage "$stage" \
      --arg expected_uid "$expected_uid" --slurpfile observed "$status_path" '{
        target: $target, stage: $stage, expected_pod_uid: $expected_uid,
        last_observed: $observed[0]
      }' > "$tmp" || { rm -f "$tmp"; return 1; }
  elif ! jq -n --arg target "$target" --arg stage "$stage" \
      --arg expected_uid "$expected_uid" '{
        target: $target, stage: $stage, expected_pod_uid: $expected_uid,
        last_observed: null
      }' > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  mv -f "$tmp" "$path"
}

# Arguments: ready|terminal, budget seconds, namespace, Pod name, exact Pod UID,
# container name, pinned image reference, last-status receipt, failure receipt,
# reader function, spec-validator function. The reader must honor its request
# timeout argument and print one Pod JSON object. Both callbacks are read-only.
# ARM_KUBE_POLL_STAGE is set for the caller's own failure receipt.
arm_kube_poll_pod_status() {
  local target=$1 budget=$2 namespace=$3 pod=$4 expected_uid=$5
  local container=$6 image=$7 status_path=$8 failure_path=$9
  local reader=${10} validator=${11} digest deadline remaining observed
  local phase exit_code ready image_id stage=timeout

  ARM_KUBE_POLL_STAGE=invalid_arguments
  [[ "$target" == ready || "$target" == terminal ]] || return 2
  [[ "$budget" =~ ^[1-9][0-9]*$ ]] || return 2
  [[ "$image" == *@sha256:* ]] || return 2
  digest=${image##*@}
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || return 2
  declare -F "$reader" >/dev/null && declare -F "$validator" >/dev/null || return 2
  [[ "$status_path" != "$failure_path" ]] || return 2

  deadline=$((SECONDS + budget))
  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    if (( remaining > 5 )); then remaining=5; fi
    stage=read_failed
    if ! observed="$("$reader" "$remaining" "$namespace" "$pod")"; then break; fi
    stage=receipt_failed
    if ! arm_kube_status_receipt "$status_path" "$observed" "$expected_uid"; then break; fi
    stage=identity_mismatch
    if ! jq -e --arg namespace "$namespace" --arg pod "$pod" \
        --arg uid "$expected_uid" --arg container "$container" \
        --arg image "$image" --arg digest "$digest" '
          .metadata.namespace == $namespace and .metadata.name == $pod and
          .metadata.uid == $uid and
          (.spec.containers | length) == 1 and
          .spec.containers[0].name == $container and
          .spec.containers[0].image == $image and
          ((.status.containerStatuses // []) | length <= 1) and
          ((.status.containerStatuses // []) | all(
            .name == $container and
            (((.imageID // "") == "") or ((.imageID // "") | endswith($digest)))
          ))
        ' <<< "$observed" >/dev/null; then break; fi
    stage=spec_mismatch
    if ! "$validator" "$observed"; then break; fi

    phase="$(jq -r '.status.phase // ""' <<< "$observed")"
    exit_code="$(jq -r '.status.containerStatuses[0].state.terminated.exitCode // ""' <<< "$observed")"
    image_id="$(jq -r '.status.containerStatuses[0].imageID // ""' <<< "$observed")"
    stage=pod_failed
    if [[ "$phase" == Failed || ( -n "$exit_code" && "$exit_code" != 0 ) ]]; then break; fi

    ready=false
    if [[ "$target" == ready ]]; then
      ready="$(jq -r '(.status.conditions // []) | any(.type == "Ready" and .status == "True")' <<< "$observed")"
    fi
    if [[ "$ready" == true || ( "$phase" == Succeeded && "$exit_code" == 0 ) ]]; then
      stage=image_id_mismatch
      if [[ "$image_id" != *"$digest" ]]; then break; fi
      ARM_KUBE_POLL_STAGE=success
      return 0
    fi
    stage=timeout
    sleep 1
  done
  ARM_KUBE_POLL_STAGE=$stage
  arm_kube_failure_receipt "$failure_path" "$target" "$ARM_KUBE_POLL_STAGE" \
    "$expected_uid" "$status_path" || return 1
  return 1
}
