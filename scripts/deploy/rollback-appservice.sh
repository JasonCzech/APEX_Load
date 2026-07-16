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
prior_commit="${5:-}"
deployed_commit="${6:-}"
staging_url="${7:-}"
swap_state_file="${8:-}"

[[ -n "$resource_group" && -n "$app_name" && -n "$slot" ]] || die "resource group, app, and slot are required"
[[ "$production_url" == https://* ]] || die "production URL must use HTTPS"
[[ "$staging_url" == https://* ]] || die "staging URL must use HTTPS"
[[ -n "$swap_state_file" ]] || die "durable swap-state path is required"
[[ "$prior_commit" =~ ^[0-9a-f]{40}$ && "$deployed_commit" =~ ^[0-9a-f]{40}$ ]] \
  || die "prior and deployed commits must be full SHAs"

state=absent
if [[ -f "$swap_state_file" ]]; then
  state="$(tr -d '[:space:]' < "$swap_state_file")"
fi
case "$state" in
  absent|swap-started|swap-completed|rollback-started|rolled-back) ;;
  *) die "invalid durable swap state" ;;
esac

read_commit() {
  curl --fail --silent --show-error --retry 3 \
    "${1%/}/api/v1/system/health" | jq -r '.commit // empty' 2>/dev/null || true
}
production_commit="$(read_commit "$production_url")"
staging_commit="$(read_commit "$staging_url")"
needs_swap=0
if [[ "$production_commit" == "$prior_commit" || "$state" == rolled-back ]]; then
  needs_swap=0
elif [[ "$production_commit" == "$deployed_commit" || "$staging_commit" == "$prior_commit" ]]; then
  needs_swap=1
elif [[ "$staging_commit" == "$deployed_commit" ]]; then
  needs_swap=0
elif [[ "$state" == swap-completed ]]; then
  needs_swap=1
else
  die "could not safely determine slot state (production=${production_commit:-unavailable}, staging=${staging_commit:-unavailable}, state=$state)"
fi

if (( needs_swap == 1 )); then
  printf 'rollback-started\n' > "$swap_state_file"
  az webapp deployment slot swap \
    --resource-group "$resource_group" \
    --name "$app_name" \
    --slot "$slot" \
    --target-slot production >/dev/null
  printf 'rolled-back\n' > "$swap_state_file"
fi

"$root_dir/scripts/deploy/controller-smoke.sh" "$production_url" "$prior_commit"
printf 'App Service production restored to commit %s\n' "$prior_commit"
