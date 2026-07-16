#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'App Service deployment: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
active_run_override_approval="${APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL:-}"
unset APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL
resource_group="${1:-}"
app_name="${2:-}"
slot="${3:-staging}"
package="${4:-}"
expected_commit="${5:-}"
staging_url="${6:-}"
production_url="${7:-}"
swap_state_file="${8:-}"

[[ -n "$resource_group" && -n "$app_name" && -n "$slot" ]] || die "resource group, app name, and slot are required"
[[ -f "$package" ]] || die "verified App Service package is missing"
[[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || die "expected commit is invalid"
[[ "$staging_url" == https://* && "$production_url" == https://* ]] \
  || die "staging and production URLs must use HTTPS"
[[ -n "$swap_state_file" ]] || die "a durable swap-state path is required"
mkdir -p "$(dirname "$swap_state_file")"
rm -f "$swap_state_file"

prior_commit="$(curl --fail --silent --show-error --retry 3 \
  "${production_url%/}/api/v1/system/health" | jq -r '.commit // empty')"
[[ "$prior_commit" =~ ^[0-9a-f]{40}$ ]] \
  || die "could not capture a verifiable production commit for automatic rollback"

az webapp deploy \
  --resource-group "$resource_group" \
  --name "$app_name" \
  --slot "$slot" \
  --src-path "$package" \
  --type zip \
  --clean true \
  --restart true >/dev/null

"$root_dir/scripts/deploy/controller-smoke.sh" "$staging_url" "$expected_commit"
python3 "$root_dir/scripts/deploy/websocket-smoke.py" \
  "${staging_url/https:/wss:}/api/v1/ws" \
  --token "${APEXLOAD_DEPLOY_API_KEY:-}"
APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL="$active_run_override_approval" \
  "$root_dir/scripts/deploy/active-runs-preflight.sh" "$production_url"
active_run_override_approval=''

swap_completed=0
deployment_verified=0
rollback_swap() {
  local exit_code=$?
  trap - EXIT INT TERM
  if (( swap_completed == 0 || deployment_verified == 1 )); then
    return
  fi
  set +e
  printf 'rollback-started\n' > "$swap_state_file"
  printf 'production verification failed; swapping %s back immediately\n' "$slot" >&2
  if az webapp deployment slot swap \
    --resource-group "$resource_group" \
    --name "$app_name" \
    --slot "$slot" \
    --target-slot production >/dev/null; then
    printf 'rolled-back\n' > "$swap_state_file"
    "$root_dir/scripts/deploy/controller-smoke.sh" "$production_url" "$prior_commit" || true
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
"$root_dir/scripts/deploy/controller-smoke.sh" "$production_url" "$expected_commit"
python3 "$root_dir/scripts/deploy/websocket-smoke.py" \
  "${production_url/https:/wss:}/api/v1/ws" \
  --token "${APEXLOAD_DEPLOY_API_KEY:-}"
deployment_verified=1
trap - EXIT INT TERM

printf 'App Service package passed staging, swap, and production verification\n'
