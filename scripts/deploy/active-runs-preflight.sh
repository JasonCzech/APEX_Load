#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'deployment lease preflight: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
controller_url="${1:-${APEXLOAD_CONTROLLER_URL:-}}"
deploy_api_key="${APEXLOAD_DEPLOY_API_KEY:-}"
active_run_override_api_key="${APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY:-}"
release="${APEXLOAD_DEPLOY_RELEASE:-}"
environment="${APEXLOAD_DEPLOY_ENVIRONMENT:-}"
actor="${APEXLOAD_DEPLOY_ACTOR:-}"
repository="${APEXLOAD_DEPLOY_REPOSITORY:-}"
server_url="${APEXLOAD_DEPLOY_SERVER_URL:-}"
run_id="${APEXLOAD_DEPLOY_RUN_ID:-}"
run_attempt="${APEXLOAD_DEPLOY_RUN_ATTEMPT:-}"
approval="${APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL:-}"
state_file="${APEXLOAD_DEPLOYMENT_LEASE_STATE_FILE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/apexload-deployment-lease.json}"
# 24m mutation + 60s termination grace + 25m fallback rollback job + 10m handoff margin.
readonly rollback_safe_ttl_seconds=3600
ttl_seconds="${APEXLOAD_DEPLOYMENT_LEASE_TTL_SECONDS:-$rollback_safe_ttl_seconds}"
unset APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL
unset APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY

# A cleanup step runs under always(). Invalidate any state left by an earlier
# job before this attempt can fail, so cleanup can never release another run's
# lease on a persistent self-hosted runner.
umask 077
mkdir -p "$(dirname "$state_file")"
rm -f -- "$state_file"
if [[ -n "${GITHUB_ENV:-}" ]]; then
  printf 'APEXLOAD_DEPLOYMENT_LEASE_TOKEN=\n' >> "$GITHUB_ENV"
fi

[[ "$controller_url" == https://* && -n "$deploy_api_key" ]] \
  || die "an HTTPS controller URL and deploy API key are required"
is_apex_release_tag "$release" || die "deployment release audit metadata is invalid"
[[ "$environment" =~ ^(aks|appservice)-(staging|production)$ ]] \
  || die "deployment environment audit metadata is invalid"
[[ -n "$actor" && "$run_id" =~ ^[1-9][0-9]*$ && "$run_attempt" =~ ^[1-9][0-9]*$ ]] \
  || die "GitHub deployment actor/run audit metadata is invalid"
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] \
  || die "GitHub deployment repository audit metadata is invalid"
[[ "$server_url" =~ ^https://[^/]+(/[^[:space:]]*)?$ ]] \
  || die "GitHub deployment server audit metadata is invalid"
if ! [[ "$ttl_seconds" =~ ^[0-9]+$ ]] || (( ttl_seconds != rollback_safe_ttl_seconds )); then
  die "deployment lease TTL must be 3600 seconds to cover bounded mutation and fallback rollback"
fi

approval_id=''
authorization_api_key="$deploy_api_key"
if [[ -n "$approval" ]]; then
  jq -e '
    type == "object" and
    keys == ["approval_id", "environment", "expires_at", "reason", "release", "schema_version"] and
    .schema_version == 1 and
    ((.approval_id | type) == "string") and
    ((.release | type) == "string") and
    ((.environment | type) == "string") and
    ((.expires_at | type) == "string") and
    ((.reason | type) == "string") and
    ((.reason | length) >= 8 and (.reason | length) <= 512)
  ' >/dev/null <<< "$approval" || die "the reviewed active-run override approval is invalid"
  approval_id="$(jq -er '.approval_id' <<< "$approval")"
  approval_release="$(jq -er '.release' <<< "$approval")"
  approval_environment="$(jq -er '.environment' <<< "$approval")"
  approval_expires="$(jq -er '.expires_at' <<< "$approval")"
  [[ "$approval_id" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{2,127}$ ]] || die "the reviewed approval id is invalid"
  [[ "$approval_release" == "$release" && "$approval_environment" == "$environment" ]] \
    || die "the reviewed approval is bound to a different release or environment"
  [[ "$approval_expires" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "the reviewed approval expiry must be UTC RFC3339"
  expiry_epoch="$(jq -er '.expires_at | fromdateiso8601' <<< "$approval")" || die "the reviewed approval expiry is invalid"
  now_epoch="$(date -u +%s)"
  (( expiry_epoch > now_epoch && expiry_epoch - now_epoch <= 14400 )) \
    || die "the reviewed approval is expired or exceeds the four-hour limit"
  [[ -n "$active_run_override_api_key" ]] \
    || die "a separately scoped active-run override API key is required with the reviewed approval"
  [[ "$active_run_override_api_key" != "$deploy_api_key" ]] \
    || die "the active-run override API key must be distinct from the ordinary deployment API key"
  authorization_api_key="$active_run_override_api_key"
fi

command -v openssl >/dev/null 2>&1 || die "openssl is required to create a deployment lease token"
if ! command -v timeout >/dev/null 2>&1 || ! timeout --version 2>/dev/null | grep -q 'GNU coreutils'; then
  die "GNU coreutils timeout is required to bound lease-held mutations"
fi
token="$(openssl rand -hex 32)"
if [[ -n "${GITHUB_ENV:-}" ]]; then
  printf '::add-mask::%s\n' "$token"
fi
owner_id="github:${repository}:${run_id}:${run_attempt}"
request_file="$(mktemp)"
response_file="$(mktemp)"
headers_file="$(mktemp)"
state_temp="$(mktemp "${state_file}.tmp.XXXXXX")"
# shellcheck disable=SC2329 # Invoked indirectly by the EXIT trap below.
cleanup() {
  rm -f -- "$request_file" "$response_file" "$headers_file"
  if [[ -n "$state_temp" ]]; then
    rm -f -- "$state_temp"
  fi
}
trap cleanup EXIT
printf 'Authorization: Bearer %s\n%s: %s\nContent-Type: application/json\n' \
  "$authorization_api_key" 'X-APEXLoad-Deployment-Lease-Token' "$token" > "$headers_file"
authorization_api_key=''
active_run_override_api_key=''
APEXLOAD_LEASE_STATE_CONTROLLER_URL="${controller_url%/}" \
APEXLOAD_LEASE_STATE_OWNER_ID="$owner_id" \
APEXLOAD_LEASE_STATE_TOKEN="$token" \
APEXLOAD_LEASE_STATE_TTL_SECONDS="$ttl_seconds" \
  jq -cn \
    '{schema_version:1,controller_url:env.APEXLOAD_LEASE_STATE_CONTROLLER_URL,owner_id:env.APEXLOAD_LEASE_STATE_OWNER_ID,token:env.APEXLOAD_LEASE_STATE_TOKEN,ttl_seconds:(env.APEXLOAD_LEASE_STATE_TTL_SECONDS | tonumber)}' \
    > "$state_temp"
chmod 600 "$state_temp"
jq -cn \
  --arg owner_id "$owner_id" \
  --arg release "$release" \
  --arg environment "$environment" \
  --arg actor "$actor" \
  --arg repository "$repository" \
  --arg server_url "$server_url" \
  --arg run_id "$run_id" \
  --arg run_attempt "$run_attempt" \
  --arg approval_id "$approval_id" \
  --argjson allow_active_tests "$([[ -n "$approval_id" ]] && printf true || printf false)" \
  --argjson ttl_seconds "$ttl_seconds" \
  '{owner_id:$owner_id,ttl_seconds:$ttl_seconds,allow_active_tests:$allow_active_tests,context:{release:$release,environment:$environment,actor:$actor,repository:$repository,server_url:$server_url,run_id:$run_id,run_attempt:$run_attempt,approval_id:$approval_id}}' \
  > "$request_file"

http_status="$(curl --silent --show-error --retry 3 --retry-all-errors \
  --connect-timeout 5 --max-time 30 \
  --output "$response_file" --write-out '%{http_code}' \
  --header "@${headers_file}" \
  --data-binary "@${request_file}" \
  "${controller_url%/}/api/v1/system/deployment-lease/acquire")" \
  || die "could not contact the controller deployment lease API"

if [[ "$http_status" == 200 ]] && jq -e --arg owner "$owner_id" '.status == "acquired" and .lease.active == true and .lease.owner_id == $owner' "$response_file" >/dev/null; then
  mv -f -- "$state_temp" "$state_file"
  state_temp=''
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    printf 'APEXLOAD_DEPLOYMENT_LEASE_TOKEN=%s\n' "$token" >> "$GITHUB_ENV"
  fi
  expires_at="$(jq -er '.lease.expires_at' "$response_file")"
  active_count="$(jq -r '.active_tests // 0' "$response_file")"
  printf 'deployment lease acquired for %s until %s (existing active tests: %s)\n' "$owner_id" "$expires_at" "$active_count"
  exit 0
fi

if [[ "$http_status" == 409 ]]; then
  active_count="$(jq -r '.active_tests // 0' "$response_file" 2>/dev/null || printf '0')"
  if [[ "$active_count" =~ ^[1-9][0-9]*$ ]]; then
    if [[ -n "$approval_id" ]]; then
      die "$active_count tests are executing; reviewed override $approval_id cannot bypass atomic lease acquisition—abort or drain the tests, then retry"
    fi
    die "$active_count tests are executing; abort or drain them, then retry"
  fi
  die "another deployment owner holds the controller lease"
fi
die "controller refused deployment lease acquisition (HTTP ${http_status})"
