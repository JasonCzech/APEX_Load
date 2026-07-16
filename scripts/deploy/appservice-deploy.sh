#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'App Service deployment: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
resource_group="${1:-}"
app_name="${2:-}"
slot="${3:-staging}"
package="${4:-}"
expected_revision="${5:-}"
staging_url="${6:-}"
production_url="${7:-}"
swap_state_file="${8:-}"
expected_version="${9:-}"
expected_build_time="${10:-}"
deployment_environment="${11:-}"

[[ -n "$resource_group" && -n "$app_name" && -n "$slot" ]] || die "resource group, app name, and slot are required"
[[ -f "$package" ]] || die "verified App Service package is missing"
[[ "$expected_revision" =~ ^[0-9a-f]{40}$ ]] || die "expected revision is invalid"
is_apex_semver "$expected_version" || die "expected release version is invalid"
[[ "$expected_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
  || die "expected release build time is invalid"
[[ "$deployment_environment" == staging || "$deployment_environment" == production ]] \
  || die "deployment environment must be staging or production"
[[ "$staging_url" == https://* ]] || die "staging URL must use HTTPS"
if [[ "$deployment_environment" == production ]]; then
  [[ "$production_url" == https://* ]] || die "production URL must use HTTPS"
else
  [[ -z "$production_url" ]] || die "staging-only deployment must not receive a production URL"
fi
[[ -n "$swap_state_file" ]] || die "a durable swap-state path is required"
mkdir -p "$(dirname "$swap_state_file")"
rm -f "$swap_state_file"

prior_revision=''
prior_version=''
prior_build_time=''
if [[ "$deployment_environment" == production ]]; then
  prior_health="$(curl --fail --silent --show-error --retry 3 --retry-all-errors \
    --connect-timeout 5 --max-time 30 \
    "${production_url%/}/api/v1/system/health")"
  prior_revision="$(jq -r '.revision // empty' <<< "$prior_health")"
  prior_version="$(jq -r '.version // empty' <<< "$prior_health")"
  prior_build_time="$(jq -r '.build_time // empty' <<< "$prior_health")"
  [[ "$prior_revision" =~ ^[0-9a-f]{40}$ ]] \
    || die "could not capture a verifiable production revision for automatic rollback"
  is_apex_semver "$prior_version" \
    || die "could not capture a verifiable production version for automatic rollback"
  [[ "$prior_build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "could not capture a verifiable production build time for automatic rollback"
fi

az webapp deploy \
  --resource-group "$resource_group" \
  --name "$app_name" \
  --slot "$slot" \
  --src-path "$package" \
  --type zip \
  --clean true \
  --restart true >/dev/null

"$root_dir/scripts/deploy/controller-smoke.sh" \
  "$staging_url" "$expected_revision" "$expected_version" "$expected_build_time"
APEXLOAD_WEBSOCKET_SMOKE_TOKEN="${APEXLOAD_DEPLOY_API_KEY:-}" \
  python3 "$root_dir/scripts/deploy/websocket-smoke.py" \
    "${staging_url/https:/wss:}/api/v1/ws"

if [[ "$deployment_environment" == staging ]]; then
  printf 'App Service package passed staging verification without a production slot swap\n'
  exit 0
fi

swap_completed=0
deployment_verified=0
rollback_swap() {
  local exit_code=$?
  trap - EXIT INT TERM
  if (( swap_completed == 0 || deployment_verified == 1 )); then
    return
  fi
  set +e
  if ! "$root_dir/scripts/deploy/deployment-lease.sh" renew; then
    printf 'deployment lease renewal was unavailable through the failed candidate; proceeding with captured App Service rollback ownership\n' >&2
  fi
  printf 'rollback-started\n' > "$swap_state_file"
  printf 'production verification failed; swapping %s back immediately\n' "$slot" >&2
  if az webapp deployment slot swap \
    --resource-group "$resource_group" \
    --name "$app_name" \
    --slot "$slot" \
    --target-slot production >/dev/null; then
    "$root_dir/scripts/deploy/deployment-lease.sh" renew || true
    if "$root_dir/scripts/deploy/controller-smoke.sh" \
      "$production_url" "$prior_revision" "$prior_version" "$prior_build_time"; then
      printf 'rolled-back\n' > "$swap_state_file"
    else
      printf 'rollback-unverified\n' > "$swap_state_file"
      printf 'automatic App Service swap-back completed but production did not verify\n' >&2
    fi
  else
    printf 'automatic App Service swap-back command failed\n' >&2
  fi
  exit "$exit_code"
}
trap rollback_swap EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'swap-started\n' > "$swap_state_file"
az webapp deployment slot swap \
  --resource-group "$resource_group" \
  --name "$app_name" \
  --slot "$slot" \
  --target-slot production >/dev/null
swap_completed=1
printf 'swap-completed\n' > "$swap_state_file"
"$root_dir/scripts/deploy/controller-smoke.sh" \
  "$production_url" "$expected_revision" "$expected_version" "$expected_build_time"
APEXLOAD_WEBSOCKET_SMOKE_TOKEN="${APEXLOAD_DEPLOY_API_KEY:-}" \
  python3 "$root_dir/scripts/deploy/websocket-smoke.py" \
    "${production_url/https:/wss:}/api/v1/ws"
deployment_verified=1
trap - EXIT INT TERM

printf 'App Service package passed staging, swap, and production verification\n'
