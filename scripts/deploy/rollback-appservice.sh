#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'App Service rollback: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
resource_group="${1:-}"
app_name="${2:-}"
slot="${3:-}"
production_url="${4:-}"
prior_revision="${5:-}"
deployed_revision="${6:-}"
staging_url="${7:-}"
swap_state_file="${8:-}"
prior_version="${9:-}"
prior_build_time="${10:-}"
deployed_version="${11:-}"
deployed_build_time="${12:-}"
readonly command_kill_grace=30s
readonly identity_timeout=75s
readonly swap_timeout=10m
readonly controller_smoke_timeout=8m

[[ -n "$resource_group" && -n "$app_name" && -n "$slot" ]] || die "resource group, app, and slot are required"
[[ "$production_url" == https://* ]] || die "production URL must use HTTPS"
[[ "$staging_url" == https://* ]] || die "staging URL must use HTTPS"
[[ -n "$swap_state_file" ]] || die "durable swap-state path is required"
[[ "$prior_revision" =~ ^[0-9a-f]{40}$ && "$deployed_revision" =~ ^[0-9a-f]{40}$ ]] \
  || die "prior and deployed revisions must be full SHAs"
source "$root_dir/scripts/release/semver.sh"
if ! is_apex_semver "$prior_version" || ! is_apex_semver "$deployed_version"; then
  die "prior and deployed versions must be canonical semantic versions"
fi
for build_time in "$prior_build_time" "$deployed_build_time"; do
  [[ "$build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "prior and deployed build times must be UTC RFC3339"
done

state=absent
if [[ -f "$swap_state_file" ]]; then
  state="$(tr -d '[:space:]' < "$swap_state_file")"
fi
case "$state" in
  absent|swap-started|swap-completed|rollback-started|rollback-unverified|rolled-back) ;;
  *) die "invalid durable swap state" ;;
esac

read_identity() {
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$identity_timeout" \
    curl --fail --silent --show-error --retry 3 --retry-all-errors --retry-max-time 60 \
      --connect-timeout 5 --max-time 30 \
      "${1%/}/api/v1/system/health" |
    jq -er '[.revision // "", .version // "", .build_time // ""] | @tsv' 2>/dev/null || true
}
production_identity="$(read_identity "$production_url")"
staging_identity="$(read_identity "$staging_url")"
IFS=$'\t' read -r production_revision production_version production_build_time <<< "$production_identity"
IFS=$'\t' read -r staging_revision staging_version staging_build_time <<< "$staging_identity"
production_is_prior=0
production_is_deployed=0
staging_is_prior=0
staging_is_deployed=0
[[ "$production_revision" == "$prior_revision" &&
   "$production_version" == "$prior_version" &&
   "$production_build_time" == "$prior_build_time" ]] && production_is_prior=1
[[ "$production_revision" == "$deployed_revision" &&
   "$production_version" == "$deployed_version" &&
   "$production_build_time" == "$deployed_build_time" ]] && production_is_deployed=1
[[ "$staging_revision" == "$prior_revision" &&
   "$staging_version" == "$prior_version" &&
   "$staging_build_time" == "$prior_build_time" ]] && staging_is_prior=1
[[ "$staging_revision" == "$deployed_revision" &&
   "$staging_version" == "$deployed_version" &&
   "$staging_build_time" == "$deployed_build_time" ]] && staging_is_deployed=1
needs_swap=0
if (( production_is_prior == 1 )); then
  needs_swap=0
elif (( production_is_deployed == 1 || staging_is_prior == 1 )); then
  needs_swap=1
elif (( staging_is_deployed == 1 )); then
  needs_swap=0
elif [[ "$state" == swap-completed ]]; then
  needs_swap=1
else
  die "could not safely determine slot state from full build metadata (production=${production_revision:-unavailable}/${production_version:-unavailable}, staging=${staging_revision:-unavailable}/${staging_version:-unavailable}, state=$state)"
fi

if (( needs_swap == 1 )); then
  printf 'rollback-started\n' > "$swap_state_file"
  timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$swap_timeout" \
    az webapp deployment slot swap \
      --resource-group "$resource_group" \
      --name "$app_name" \
      --slot "$slot" \
      --target-slot production >/dev/null
  printf 'rollback-unverified\n' > "$swap_state_file"
fi

timeout --foreground --signal=TERM --kill-after="$command_kill_grace" "$controller_smoke_timeout" \
  "$root_dir/scripts/deploy/controller-smoke.sh" \
    "$production_url" "$prior_revision" "$prior_version" "$prior_build_time"
printf 'rolled-back\n' > "$swap_state_file"
printf 'App Service production restored to revision %s\n' "$prior_revision"
