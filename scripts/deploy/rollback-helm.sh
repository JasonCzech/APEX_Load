#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
release_name="${1:?release name is required}"
namespace="${2:?namespace is required}"
revision="${3:?previous revision is required}"
controller_url="${4:-}"
expected_commit="${5:-}"

[[ "$revision" =~ ^(0|[1-9][0-9]*)$ ]] || {
  printf 'cannot roll back %s: invalid previous Helm revision %s\n' "$release_name" "$revision" >&2
  exit 1
}
if [[ "$revision" == 0 ]]; then
  printf 'ensuring failed first installation of Helm release %s is absent\n' "$release_name" >&2
  if helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1; then
    helm uninstall "$release_name" --namespace "$namespace"
  fi
  if helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1; then
    printf 'failed first installation is still present after uninstall\n' >&2
    exit 1
  fi
  for workload in deployment/apexload-controller statefulset/apexload-generator; do
    if kubectl --namespace "$namespace" get "$workload" >/dev/null 2>&1; then
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
current_revision="$(helm status "$release_name" --namespace "$namespace" -o json | jq -er '.version')"
[[ "$current_revision" =~ ^[1-9][0-9]*$ ]] || {
  printf 'could not establish the current Helm revision before recovery\n' >&2
  exit 1
}
if [[ "$current_revision" != "$revision" ]]; then
  helm rollback "$release_name" "$revision" --namespace "$namespace" --wait --timeout 10m
fi
kubectl --namespace "$namespace" rollout status deployment/apexload-controller --timeout=5m
kubectl --namespace "$namespace" rollout status statefulset/apexload-generator --timeout=5m
"$root_dir/scripts/deploy/controller-smoke.sh" "$controller_url" "$expected_commit"
printf 'restored Helm release %s to revision %s\n' "$release_name" "$revision"
