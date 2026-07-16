#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'Kubernetes deployment: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
release_name="${1:-}"
namespace="${2:-}"
chart="${3:-}"
values_file="${4:-}"
controller_image="${5:-}"
generator_image="${6:-}"
expected_revision="${7:-}"
controller_url="${8:-}"
controller_platforms="${9:-}"
generator_platforms="${10:-}"
expected_version="${11:-}"
expected_build_time="${12:-}"

[[ "$release_name" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || die "invalid Helm release name"
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || die "invalid namespace"
[[ -f "$chart" && -f "$values_file" ]] || die "chart and values files are required"
[[ "$controller_image" =~ @sha256:[0-9a-f]{64}$ ]] || die "controller image is not immutable"
[[ "$generator_image" =~ @sha256:[0-9a-f]{64}$ ]] || die "generator image is not immutable"
[[ "$expected_revision" =~ ^[0-9a-f]{40}$ ]] || die "expected revision is invalid"
[[ "$controller_url" == https://* ]] || die "controller URL must use HTTPS"
is_apex_semver "$expected_version" || die "expected release version is invalid"
[[ "$expected_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
  || die "expected release build time is invalid"
validate_platform_map() {
  jq -e '
    type == "object" and
    (keys | sort == ["amd64", "arm64"]) and
    all(.[]; type == "string" and test("^sha256:[0-9a-f]{64}$"))
  ' <<< "$1" >/dev/null || die "signed platform digest map is invalid"
}
validate_platform_map "$controller_platforms"
validate_platform_map "$generator_platforms"

export APEXLOAD_CONTROLLER_IMAGE="$controller_image"
export APEXLOAD_GENERATOR_IMAGE="$generator_image"
post_renderer="$root_dir/scripts/deploy/helm-digest-post-renderer.py"
rendered="$(mktemp)"
identity_values="$(mktemp)"
canonical_controller_url="$(
  python3 - "$controller_url" <<'PY'
import sys
import urllib.parse

parsed = urllib.parse.urlsplit(sys.argv[1].strip())
if (
    parsed.scheme.lower() != "https"
    or not parsed.hostname
    or parsed.username is not None
    or parsed.password is not None
    or parsed.query
    or parsed.fragment
):
    raise SystemExit(1)
try:
    port = parsed.port
except ValueError:
    raise SystemExit(1)
host = parsed.hostname.lower()
if ":" in host:
    host = f"[{host}]"
if port not in (None, 443):
    host = f"{host}:{port}"
print(urllib.parse.urlunsplit(("https", host, parsed.path.rstrip("/"), "", "")))
PY
)" || die "controller URL is not a valid public HTTPS URL"
jq -n --arg public_url "$canonical_controller_url" \
  '{controller:{publicURL:$public_url}}' > "$identity_values"
trap 'rm -f "$rendered" "$identity_values"' EXIT

previous_revision="$(helm status "$release_name" --namespace "$namespace" -o json 2>/dev/null \
  | jq -r '.version // 0' || printf '0')"
previous_controller="$(kubectl --namespace "$namespace" get deployment apexload-controller \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}' 2>/dev/null || true)"
previous_generator="$(kubectl --namespace "$namespace" get statefulset apexload-generator \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="generator")].image}' 2>/dev/null || true)"
previous_controller_revision=''
previous_controller_version=''
previous_controller_build_time=''
if [[ "$previous_revision" =~ ^[1-9][0-9]*$ ]]; then
  previous_health="$(curl --fail --silent --show-error --retry 3 --retry-all-errors \
    --connect-timeout 5 --max-time 30 \
    "${controller_url%/}/api/v1/system/health")"
  previous_controller_revision="$(jq -r '.revision // empty' <<< "$previous_health")"
  previous_controller_version="$(jq -r '.version // empty' <<< "$previous_health")"
  previous_controller_build_time="$(jq -r '.build_time // empty' <<< "$previous_health")"
  [[ "$previous_controller_revision" =~ ^[0-9a-f]{40}$ ]] \
    || die "could not capture a verifiable controller revision for automatic rollback"
  is_apex_semver "$previous_controller_version" \
    || die "could not capture a verifiable controller version for automatic rollback"
  [[ "$previous_controller_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "could not capture a verifiable controller build time for automatic rollback"
  [[ -n "$previous_controller" && -n "$previous_generator" ]] \
    || die "could not capture prior workload images for automatic rollback"
fi

mutation_started=0
deployment_verified=0

rollback() {
  local exit_code=$?
  trap - EXIT INT TERM
  rm -f "$rendered" "$identity_values"
  if (( mutation_started == 0 || deployment_verified == 1 )); then
    return
  fi
  set +e
  if ! "$root_dir/scripts/deploy/deployment-lease.sh" renew; then
    printf 'deployment lease renewal was unavailable through the failed candidate; proceeding with captured rollback ownership\n' >&2
  fi
  rollback_failed=0
  if [[ "$previous_revision" =~ ^[1-9][0-9]*$ ]]; then
    printf 'post-deploy verification failed; rolling back to Helm revision %s\n' "$previous_revision" >&2
    helm rollback "$release_name" "$previous_revision" --namespace "$namespace" --wait --timeout 10m \
      || rollback_failed=1
    kubectl --namespace "$namespace" rollout status deployment/apexload-controller --timeout=5m \
      || rollback_failed=1
    kubectl --namespace "$namespace" rollout status statefulset/apexload-generator --timeout=5m \
      || rollback_failed=1
    current_controller="$(kubectl --namespace "$namespace" get deployment apexload-controller -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}' 2>/dev/null || true)"
    current_generator="$(kubectl --namespace "$namespace" get statefulset apexload-generator -o jsonpath='{.spec.template.spec.containers[?(@.name=="generator")].image}' 2>/dev/null || true)"
    [[ "$current_controller" == "$previous_controller" && "$current_generator" == "$previous_generator" ]] \
      || rollback_failed=1
    "$root_dir/scripts/deploy/deployment-lease.sh" renew || rollback_failed=1
    "$root_dir/scripts/deploy/controller-smoke.sh" \
      "$controller_url" \
      "$previous_controller_revision" \
      "$previous_controller_version" \
      "$previous_controller_build_time" \
      || rollback_failed=1
  else
    printf 'first installation failed; uninstalling incomplete Helm release\n' >&2
    if helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1; then
      helm uninstall "$release_name" --namespace "$namespace" || rollback_failed=1
    fi
    helm status "$release_name" --namespace "$namespace" >/dev/null 2>&1 && rollback_failed=1
    kubectl --namespace "$namespace" get deployment/apexload-controller statefulset/apexload-generator >/dev/null 2>&1 \
      && rollback_failed=1
  fi
  (( rollback_failed == 0 )) || printf 'automatic rollback revalidation failed\n' >&2
  exit "$exit_code"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

namespace_overrides=(--set namespace.create=false --set-string "namespace.name=$namespace")
helm lint "$chart" --namespace "$namespace" --values "$values_file" --values "$identity_values" "${namespace_overrides[@]}"
helm template "$release_name" "$chart" \
  --namespace "$namespace" \
  --values "$values_file" \
  --values "$identity_values" \
  "${namespace_overrides[@]}" \
  --post-renderer "$post_renderer" > "$rendered"
if grep -Eq '^kind:[[:space:]]*Namespace[[:space:]]*$' "$rendered"; then
  die "production deployments must not create or mutate the preflighted namespace"
fi
if grep -Eq '^kind:[[:space:]]*Secret[[:space:]]*$' "$rendered"; then
  die "production deployments must not render Kubernetes Secret resources; provision secrets externally during preflight"
fi
if ! awk -v expected="$namespace" '
  /^[[:space:]]*namespace:[[:space:]]*/ {
    value = $0
    sub(/^[[:space:]]*namespace:[[:space:]]*/, "", value)
    gsub(/["'\'']/, "", value)
    if (value != expected) exit 1
  }
' "$rendered"; then
  die "rendered resource targets a namespace other than the reviewed deployment namespace"
fi
grep -Fq "$controller_image" "$rendered" || die "rendered controller digest is missing"
grep -Fq "$generator_image" "$rendered" || die "rendered generator digest is missing"
kubectl apply --dry-run=server --namespace "$namespace" -f "$rendered" >/dev/null
set +e
kubectl diff --namespace "$namespace" -f "$rendered"
diff_status=$?
set -e
(( diff_status <= 1 )) || die "kubectl diff failed"

mutation_started=1
helm upgrade --install "$release_name" "$chart" \
  --namespace "$namespace" \
  --values "$values_file" \
  --values "$identity_values" \
  "${namespace_overrides[@]}" \
  --post-renderer "$post_renderer" \
  --atomic --wait --timeout 10m --history-max 20
kubectl --namespace "$namespace" rollout status deployment/apexload-controller --timeout=5m
kubectl --namespace "$namespace" rollout status statefulset/apexload-generator --timeout=5m

nodes="$(kubectl get nodes -o json)"
verify_pod_platform_digests() {
  local selector="$1"
  local container_name="$2"
  local platform_map="$3"
  local pods
  pods="$(kubectl --namespace "$namespace" get pods -l "$selector" -o json)"
  jq -e \
    --arg container "$container_name" \
    --argjson expected "$platform_map" \
    --argjson nodes "$nodes" '
      def node_arch($name):
        ([ $nodes.items[]
          | select(.metadata.name == $name)
          | .metadata.labels["kubernetes.io/arch"] ]
        | if length == 1 then .[0] else null end);
      (.items | length) > 0 and
      all(.items[];
        . as $pod |
        node_arch($pod.spec.nodeName) as $arch |
        ($expected[$arch] // "") as $digest |
        ($digest | test("^sha256:[0-9a-f]{64}$")) and
        ([.status.containerStatuses[]? | select(.name == $container)] | length == 1) and
        any(.status.containerStatuses[]?;
          .name == $container and (.imageID | endswith($digest)))
      )
    ' <<< "$pods" >/dev/null \
    || die "$container_name pod imageID does not match its node architecture digest in the release manifest"
}
verify_pod_platform_digests 'app.kubernetes.io/name=apexload-controller' controller "$controller_platforms"
verify_pod_platform_digests 'app.kubernetes.io/name=apexload-generator' generator "$generator_platforms"

"$root_dir/scripts/deploy/controller-smoke.sh" \
  "$controller_url" "$expected_revision" "$expected_version" "$expected_build_time"
deployment_verified=1
trap - EXIT INT TERM
rm -f "$rendered" "$identity_values"
printf 'deployed and verified Helm release %s at immutable image digests\n' "$release_name"
