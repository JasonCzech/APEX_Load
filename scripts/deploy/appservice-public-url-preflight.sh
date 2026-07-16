#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'App Service public URL preflight: %s\n' "$*" >&2
  exit 1
}

resource_group="${1:-}"
app_name="${2:-}"
slot="${3:-}"
staging_url="${4:-}"
deployment_environment="${5:-}"
production_url="${6:-}"

[[ -n "$resource_group" && -n "$app_name" && -n "$slot" ]] \
  || die "resource group, app name, and staging slot are required"
[[ "$slot" != production ]] || die "the staging slot must not be named production"
[[ "$deployment_environment" == staging || "$deployment_environment" == production ]] \
  || die "deployment environment must be staging or production"

normalize_public_url() {
  python3 - "$1" <<'PY'
import sys
import urllib.parse

raw = sys.argv[1].strip()
parsed = urllib.parse.urlsplit(raw)
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

path = parsed.path.rstrip("/")
print(urllib.parse.urlunsplit(("https", host, path, "", "")))
PY
}

expected_staging="$(normalize_public_url "$staging_url")" \
  || die "staging URL must be an absolute HTTPS URL without credentials, query, or fragment"
[[ -n "$expected_staging" ]] || die "staging URL is empty"

expected_production=''
if [[ "$deployment_environment" == production ]]; then
  expected_production="$(normalize_public_url "$production_url")" \
    || die "production URL must be an absolute HTTPS URL without credentials, query, or fragment"
  [[ -n "$expected_production" ]] || die "production URL is empty"
else
  [[ -z "$production_url" ]] || die "staging-only validation must not receive a production URL"
fi

validate_slot_public_url() {
  local slot_name="$1"
  local expected="$2"
  local label="$3"
  local settings
  if [[ -n "$slot_name" ]]; then
    settings="$(
      az webapp config appsettings list \
        --resource-group "$resource_group" \
        --name "$app_name" \
        --slot "$slot_name" \
        --query "[?name=='APEXLOAD_PUBLIC_URL']" \
        --output json \
        --only-show-errors
    )" || die "could not read $label App Service settings"
  else
    settings="$(
      az webapp config appsettings list \
        --resource-group "$resource_group" \
        --name "$app_name" \
        --query "[?name=='APEXLOAD_PUBLIC_URL']" \
        --output json \
        --only-show-errors
    )" || die "could not read $label App Service settings"
  fi

  [[ "$(jq -r 'length' <<< "$settings")" == 1 ]] \
    || die "$label must define exactly one APEXLOAD_PUBLIC_URL app setting"
  jq -e '.[0].slotSetting == true' <<< "$settings" >/dev/null \
    || die "$label APEXLOAD_PUBLIC_URL must be marked as a deployment-slot setting"

  local configured
  configured="$(jq -er '.[0].value | strings' <<< "$settings")" \
    || die "$label APEXLOAD_PUBLIC_URL must have a string value"
  configured="$(normalize_public_url "$configured")" \
    || die "$label APEXLOAD_PUBLIC_URL is not a valid public HTTPS URL"
  [[ "$configured" == "$expected" ]] \
    || die "$label APEXLOAD_PUBLIC_URL does not match the protected environment URL"
}

validate_slot_public_url "$slot" "$expected_staging" "staging slot"
if [[ "$deployment_environment" == production ]]; then
  validate_slot_public_url "" "$expected_production" "production slot"
fi

printf 'App Service slot-bound APEXLOAD_PUBLIC_URL settings verified\n'
