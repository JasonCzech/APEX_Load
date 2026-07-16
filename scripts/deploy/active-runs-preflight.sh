#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'active-run preflight: %s\n' "$*" >&2
  exit 1
}

approve_active_runs() {
  local active_count="$1"
  local approval="$2"
  local release="${APEXLOAD_DEPLOY_RELEASE:-}"
  local environment="${APEXLOAD_DEPLOY_ENVIRONMENT:-}"
  local actor="${APEXLOAD_DEPLOY_ACTOR:-}"
  local repository="${APEXLOAD_DEPLOY_REPOSITORY:-}"
  local server_url="${APEXLOAD_DEPLOY_SERVER_URL:-}"
  local run_id="${APEXLOAD_DEPLOY_RUN_ID:-}"
  local run_attempt="${APEXLOAD_DEPLOY_RUN_ATTEMPT:-}"

  [[ -n "$approval" ]] \
    || die "$active_count tests are active; drain/abort them or configure a reviewed protected-environment override"
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
  ' >/dev/null <<< "$approval" \
    || die "the reviewed active-run override approval is invalid"

  local approval_id approval_release approval_environment approval_expires expiry_epoch now_epoch
  approval_id="$(jq -er '.approval_id' <<< "$approval")" \
    || die "the reviewed active-run override approval id is invalid"
  approval_release="$(jq -er '.release' <<< "$approval")" \
    || die "the reviewed active-run override release is invalid"
  approval_environment="$(jq -er '.environment' <<< "$approval")" \
    || die "the reviewed active-run override environment is invalid"
  approval_expires="$(jq -er '.expires_at' <<< "$approval")" \
    || die "the reviewed active-run override expiry is invalid"

  [[ "$approval_id" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{2,127}$ ]] \
    || die "the reviewed active-run override approval id is invalid"
  [[ "$release" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]] \
    || die "deployment release audit metadata is invalid"
  [[ "$environment" =~ ^(aks|appservice)-(staging|production)$ ]] \
    || die "deployment environment audit metadata is invalid"
  [[ "$approval_release" == "$release" ]] \
    || die "the reviewed active-run override is bound to a different release"
  [[ "$approval_environment" == "$environment" ]] \
    || die "the reviewed active-run override is bound to a different environment"
  [[ "$approval_expires" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
    || die "the reviewed active-run override expiry must be UTC RFC3339"
  expiry_epoch="$(jq -er '.expires_at | fromdateiso8601' <<< "$approval")" \
    || die "the reviewed active-run override expiry is invalid"
  now_epoch="$(date -u +%s)"
  (( expiry_epoch > now_epoch )) \
    || die "the reviewed active-run override has expired"
  (( expiry_epoch - now_epoch <= 14400 )) \
    || die "the reviewed active-run override expiry exceeds the four-hour limit"

  [[ -n "$actor" && "$run_id" =~ ^[1-9][0-9]*$ && "$run_attempt" =~ ^[1-9][0-9]*$ ]] \
    || die "GitHub deployment actor/run audit metadata is invalid"
  [[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] \
    || die "GitHub deployment repository audit metadata is invalid"
  [[ "$server_url" =~ ^https://[^/]+(/[^[:space:]]*)?$ ]] \
    || die "GitHub deployment server audit metadata is invalid"

  local run_url audit
  run_url="${server_url%/}/${repository}/actions/runs/${run_id}"
  audit="$(jq -cn \
    --arg event "active_run_override_accepted" \
    --arg approval_id "$approval_id" \
    --arg release "$release" \
    --arg environment "$environment" \
    --arg actor "$actor" \
    --arg run_url "$run_url" \
    --arg run_attempt "$run_attempt" \
    --arg expires_at "$approval_expires" \
    --argjson active_tests "$active_count" \
    '{event: $event, approval_id: $approval_id, active_tests: $active_tests, release: $release, environment: $environment, actor: $actor, run_url: $run_url, run_attempt: $run_attempt, expires_at: $expires_at}')"
  printf 'active-run preflight audit: %s\n' "$audit" >&2
  printf 'reviewed active-run override accepted for %d active tests\n' "$active_count"
}

controller_url="${1:-${APEXLOAD_CONTROLLER_URL:-}}"
deploy_api_key="${APEXLOAD_DEPLOY_API_KEY:-}"
active_run_override_approval="${APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL:-}"
unset APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL
[[ "$controller_url" == https://* && -n "$deploy_api_key" ]] \
  || die "an HTTPS controller URL and deploy API key are required"

tests_json="$(curl --fail --silent --show-error --retry 3 \
  -H "Authorization: Bearer ${deploy_api_key}" \
  "${controller_url%/}/api/v1/tests?active_only=true&summary=true&limit=1")" \
  || die "could not query active tests"
active_count="$(jq -er '
  if type == "object"
     and (.total | type) == "number"
     and (.total | floor) == .total
     and .total >= 0
     and (.tests | type) == "array"
  then .total
  else error("invalid active-test response")
  end
' <<< "$tests_json")" || die "controller returned an invalid active-test response"
if (( active_count > 0 )); then
  approve_active_runs "$active_count" "$active_run_override_approval"
  active_run_override_approval=''
  exit 0
fi

printf 'zero-active-test invariant verified\n'
