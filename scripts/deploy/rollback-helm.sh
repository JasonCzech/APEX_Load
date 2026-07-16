#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
release_name="${1:?release name is required}"
namespace="${2:?namespace is required}"
revision="${3:?previous revision is required}"
controller_url="${4:-}"
expected_commit="${5:-}"
expected_version="${6:-}"
expected_build_time="${7:-}"
expected_controller_image="${8:-}"
expected_generator_image="${9:-}"
expected_controller_image_ids="${10:-}"
expected_generator_image_ids="${11:-}"
readonly command_kill_grace=30s
readonly metadata_timeout=30s
readonly helm_uninstall_timeout=5m
readonly helm_rollback_timeout=7m
readonly helm_wait_timeout=6m
readonly rollout_timeout=150s
readonly controller_smoke_timeout=6m

[[ "$release_name" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || {
  printf 'cannot roll back: invalid Helm release name\n' >&2
  exit 1
}
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || {
  printf 'cannot roll back: invalid namespace\n' >&2
  exit 1
}

[[ "$revision" =~ ^(0|[1-9][0-9]*)$ ]] || {
  printf 'cannot roll back %s: invalid previous Helm revision %s\n' "$release_name" "$revision" >&2
  exit 1
}
if [[ "$revision" == 0 ]]; then
  printf 'ensuring failed first installation of Helm release %s is absent\n' "$release_name" >&2
  if timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1; then
    timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$helm_uninstall_timeout" \
      helm uninstall "$release_name" --namespace "$namespace"
  fi
  if timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1; then
    printf 'failed first installation is still present after uninstall\n' >&2
    exit 1
  fi
  for workload in deployment/apexload-controller statefulset/apexload-generator; do
    if timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
      kubectl --namespace "$namespace" get "$workload" >/dev/null 2>&1; then
      printf 'failed first-installation workload %s is still present after uninstall\n' "$workload" >&2
      exit 1
    fi
  done
  printf 'removed failed first installation of Helm release %s\n' "$release_name"
  exit 0
fi

[[ "$controller_url" == http://* || "$controller_url" == https://* ]] || {
  printf 'controller URL is required to revalidate a restored release\n' >&2
  exit 1
}
[[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || {
  printf 'previous controller commit is required to revalidate a restored release\n' >&2
  exit 1
}
is_apex_semver "$expected_version" || {
  printf 'previous controller version is required to revalidate a restored release\n' >&2
  exit 1
}
[[ "$expected_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || {
  printf 'previous controller build time is required to revalidate a restored release\n' >&2
  exit 1
}
[[ -n "$expected_controller_image" && -n "$expected_generator_image" ]] || {
  printf 'previous workload images are required to revalidate a restored release\n' >&2
  exit 1
}
for image_ids in "$expected_controller_image_ids" "$expected_generator_image_ids"; do
  jq -e '
    type == "object" and length > 0 and
    all(to_entries[];
      (.key | test("^[a-z0-9_-]+$")) and
      (.value | test("sha256:[0-9a-f]{64}$")))
  ' <<< "$image_ids" >/dev/null || {
    printf 'captured prior pod imageIDs are required to revalidate a restored release\n' >&2
    exit 1
  }
done
current_revision="$(
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    helm status "$release_name" --namespace "$namespace" -o json |
    jq -er '.version'
)"
[[ "$current_revision" =~ ^[1-9][0-9]*$ ]] || {
  printf 'could not establish the current Helm revision before recovery\n' >&2
  exit 1
}
if [[ "$current_revision" != "$revision" ]]; then
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$helm_rollback_timeout" \
    helm rollback "$release_name" "$revision" --namespace "$namespace" --wait --timeout "$helm_wait_timeout"
fi
timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$rollout_timeout" \
  kubectl --namespace "$namespace" rollout status deployment/apexload-controller --timeout="$rollout_timeout"
timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$rollout_timeout" \
  kubectl --namespace "$namespace" rollout status statefulset/apexload-generator --timeout="$rollout_timeout"
current_controller_image="$(
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    kubectl --namespace "$namespace" get deployment apexload-controller \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}'
)"
current_generator_image="$(
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    kubectl --namespace "$namespace" get statefulset apexload-generator \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="generator")].image}'
)"
[[ "$current_controller_image" == "$expected_controller_image" &&
   "$current_generator_image" == "$expected_generator_image" ]] || {
  printf 'restored Helm revision does not match the captured workload images\n' >&2
  exit 1
}
nodes="$(
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
    kubectl get nodes -o json
)"
verify_restored_image_ids() {
  local selector="$1"
  local container="$2"
  local expected="$3"
  local pods
  pods="$(
    timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$metadata_timeout" \
      kubectl --namespace "$namespace" get pods -l "$selector" -o json
  )"
  jq -e --arg container "$container" --argjson nodes "$nodes" --argjson expected "$expected" '
    def node_arch($name):
      ([ $nodes.items[]
        | select(.metadata.name == $name)
        | .metadata.labels["kubernetes.io/arch"] ]
      | if length == 1 then .[0] else error("pod node architecture is unavailable") end);
    reduce .items[] as $pod ({};
      node_arch($pod.spec.nodeName) as $arch |
      ([ $pod.status.containerStatuses[]?
        | select(.name == $container)
        | .imageID ]
      | if length == 1 then .[0] else error("pod imageID is unavailable") end) as $image_id |
      if .[$arch] == null or .[$arch] == $image_id then .[$arch] = $image_id
      else error("multiple restored imageIDs observed for one architecture") end
    ) == $expected
  ' <<< "$pods" >/dev/null || {
    printf 'restored %s pods do not match the captured per-architecture imageIDs\n' "$container" >&2
    exit 1
  }
}
verify_restored_image_ids 'app.kubernetes.io/name=apexload-controller' controller "$expected_controller_image_ids"
verify_restored_image_ids 'app.kubernetes.io/name=apexload-generator' generator "$expected_generator_image_ids"
timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$controller_smoke_timeout" \
  "$root_dir/scripts/deploy/controller-smoke.sh" \
    "$controller_url" "$expected_commit" "$expected_version" "$expected_build_time"
printf 'restored Helm release %s to revision %s\n' "$release_name" "$revision"
