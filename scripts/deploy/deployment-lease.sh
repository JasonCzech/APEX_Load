#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'deployment lease: %s\n' "$*" >&2
  exit 1
}

operation="${1:-}"
state_file="${APEXLOAD_DEPLOYMENT_LEASE_STATE_FILE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/apexload-deployment-lease.json}"
deploy_api_key="${APEXLOAD_DEPLOY_API_KEY:-}"
[[ "$operation" == renew || "$operation" == release ]] || die "usage: deployment-lease.sh <renew|release>"

if [[ ! -f "$state_file" ]]; then
  if [[ "$operation" == release ]]; then
    printf 'deployment lease was not acquired; cleanup is a no-op\n'
    exit 0
  fi
  die "lease state file is missing"
fi

request_file=''
response_file=''
headers_file=''
scrub_release_state=false
if [[ "$operation" == release ]]; then
  scrub_release_state=true
fi
cleanup() {
  local status=$?
  local state_status=0
  local environment_status=0
  trap - EXIT
  set +e
  rm -f "$request_file" "$response_file" "$headers_file"
  if [[ "$scrub_release_state" == true ]]; then
    rm -f "$state_file"
    state_status=$?
    if [[ -n "${GITHUB_ENV:-}" ]]; then
      printf 'APEXLOAD_DEPLOYMENT_LEASE_TOKEN=\n' >> "$GITHUB_ENV"
      environment_status=$?
    fi
    if (( state_status != 0 || environment_status != 0 )); then
      printf 'deployment lease: failed to scrub local release credentials\n' >&2
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

[[ -n "$deploy_api_key" ]] || die "APEXLOAD_DEPLOY_API_KEY is required"
[[ ! -L "$state_file" ]] || die "lease state file must not be a symbolic link"
state_mode="$(stat -c '%a' "$state_file" 2>/dev/null || stat -f '%Lp' "$state_file" 2>/dev/null || true)"
[[ "$state_mode" == 600 ]] || die "lease state file permissions must be 0600"

controller_url="$(jq -er '.controller_url' "$state_file")" || die "lease state controller URL is invalid"
owner_id="$(jq -er '.owner_id' "$state_file")" || die "lease state owner is invalid"
token="$(jq -er '.token' "$state_file")" || die "lease state token is invalid"
ttl_seconds="$(jq -er '.ttl_seconds' "$state_file")" || die "lease state TTL is invalid"
[[ "$controller_url" == https://* && "$token" =~ ^[0-9a-f]{64}$ && "$ttl_seconds" =~ ^[0-9]+$ ]] \
  || die "lease state is malformed"
if [[ -n "${GITHUB_REPOSITORY:-}${GITHUB_RUN_ID:-}${GITHUB_RUN_ATTEMPT:-}" ]]; then
  [[ "${GITHUB_REPOSITORY:-}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ &&
     "${GITHUB_RUN_ID:-}" =~ ^[1-9][0-9]*$ &&
     "${GITHUB_RUN_ATTEMPT:-}" =~ ^[1-9][0-9]*$ ]] \
    || die "current GitHub run identity is incomplete or invalid"
  expected_owner="github:${GITHUB_REPOSITORY}:${GITHUB_RUN_ID}:${GITHUB_RUN_ATTEMPT}"
  [[ "$owner_id" == "$expected_owner" ]] \
    || die "lease state belongs to a different GitHub workflow attempt"
fi

umask 077
request_file="$(mktemp)"
response_file="$(mktemp)"
headers_file="$(mktemp)"
printf 'Authorization: Bearer %s\n%s: %s\nContent-Type: application/json\n' \
  "$deploy_api_key" 'X-APEXLoad-Deployment-Lease-Token' "$token" > "$headers_file"
jq -cn --arg owner_id "$owner_id" --argjson ttl_seconds "$ttl_seconds" \
  '{owner_id:$owner_id,ttl_seconds:$ttl_seconds}' > "$request_file"
http_status="$(curl --silent --show-error --retry 3 --retry-all-errors \
  --connect-timeout 5 --max-time 30 \
  --output "$response_file" --write-out '%{http_code}' \
  --header "@${headers_file}" \
  --data-binary "@${request_file}" \
  "${controller_url}/api/v1/system/deployment-lease/${operation}")" \
  || die "could not contact the controller deployment lease API"

case "$operation" in
  renew) expected_status='renewed' ;;
  release) expected_status='released' ;;
esac
if [[ "$http_status" != 200 ]] || ! jq -e --arg status "$expected_status" '.status == $status' "$response_file" >/dev/null; then
  message="$(jq -r '.error // "controller refused the request"' "$response_file" 2>/dev/null || printf 'controller refused the request')"
  die "${operation} failed (HTTP ${http_status}): ${message}"
fi
printf 'deployment lease %s for %s\n' "$expected_status" "$owner_id"
