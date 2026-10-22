#!/usr/bin/env bash
set -uo pipefail

emit_output() {
  local key="$1"
  local value="$2"
  local attempt=0
  local delimiter
  [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] || return 1
  while :; do
    delimiter="__APEXLOAD_OUTPUT_${RANDOM}_${RANDOM}_$$_${attempt}__"
    if ! printf '%s\n' "$value" | tr -d '\r' | grep -Fqx -- "$delimiter"; then
      break
    fi
    attempt=$((attempt + 1))
  done
  {
    printf '%s<<%s\n' "$key" "$delimiter"
    printf '%s\n' "$value"
    printf '%s\n' "$delimiter"
  } >> "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
}

has_apexctl_completion_marker() {
  awk '
    index($0, "apexload-evidence-complete<<") == 1 {
      delimiter = substr($0, length("apexload-evidence-complete") + 3)
      if ((getline value) > 0 && value == "true" && (getline closing) > 0 && closing == delimiter) {
        found = 1
      }
    }
    END { exit(found ? 0 : 1) }
  ' "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
}

write_fallback() {
  local outcome="$1"
  local status="$2"
  local include_summary="$3"
  case "$outcome" in
    configuration_error|execution_error) ;;
    *) outcome=execution_error ;;
  esac
  [[ "$status" =~ ^[A-Z_]+$ ]] || status=ACTION_FAILED

  printf '{\n  "schema_version": 1,\n  "outcome": "%s",\n  "status": "%s",\n  "sla_evaluated": false,\n  "sla_breached": false,\n  "transactions": 0,\n  "errors": 0,\n  "tps": 0,\n  "duration_ms": 0,\n  "message": "The APEX Load action failed before apexctl produced complete evidence."\n}\n' \
    "$outcome" "$status" > "$json_path" || return 1
  printf '%s\n' \
    '<?xml version="1.0" encoding="UTF-8"?>' \
    '<testsuites tests="1" failures="1" errors="0" skipped="0">' \
    '  <testsuite name="apexload:ci" tests="1" failures="1" errors="0" skipped="0">' \
    '    <testcase name="performance-gate" classname="apexload">' \
    '      <failure message="APEX Load action startup failure">apexctl did not produce complete evidence.</failure>' \
    '    </testcase>' \
    '  </testsuite>' \
    '</testsuites>' > "$junit_path" || return 1

  emit_output outcome "$outcome"
  emit_output test-id ""
  emit_output status "$status"
  emit_output sla-evaluated false
  emit_output sla-breached false
  emit_output transactions 0
  emit_output errors 0
  emit_output tps 0
  emit_output duration-ms 0
  emit_output report-url ""
  emit_output check-url ""
  emit_output validity ""
  emit_output root-cause ""
  emit_output json-path "$json_path"
  emit_output junit-path "$junit_path"
  if [[ "$include_summary" == true ]]; then
    printf "## APEX Load performance gate\n\n❌ %s\n\nThe action failed before \`apexctl\` produced complete evidence.\n" \
      "$outcome" >> "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}" || return 1
  fi
}

prepare_evidence_dir() {
  local candidate="$1"
  local anchor
  local relative
  local resolved
  local probe

  if [[ "$candidate" == /* ]]; then
    anchor=""
    for trusted_root in "${GITHUB_WORKSPACE:-}" "${RUNNER_TEMP:-}" "$PWD"; do
      trusted_root="${trusted_root%/}"
      [[ -n "$trusted_root" ]] || continue
      if [[ "$candidate" == "$trusted_root" ]]; then
        anchor="$trusted_root"
        relative=""
        break
      fi
      if [[ "$candidate" == "$trusted_root/"* ]]; then
        anchor="$trusted_root"
        relative="${candidate#"$trusted_root"/}"
        break
      fi
    done
    [[ -n "$anchor" ]] || return 1
  else
    anchor="$PWD"
    relative="$candidate"
  fi

  evidence_path_components_are_safe "$anchor" "$relative" || return 1

  mkdir -p -- "$candidate" 2>/dev/null || return 1
  evidence_path_components_are_safe "$anchor" "$relative" || return 1
  [[ -d "$candidate" ]] || return 1
  resolved="$(cd "$candidate" && pwd -P)" || return 1
  probe="$(mktemp "$resolved/.apexload-write-probe.XXXXXX")" || return 1
  rm -f -- "$probe" || return 1
  printf '%s' "$resolved"
}

evidence_path_components_are_safe() {
  local current="$1"
  local remaining="$2"
  local component

  # Split only on literal slash characters. Line-oriented splitting would
  # mis-parse a legal filename containing a newline and could miss a symlink.
  while :; do
    if [[ "$remaining" == */* ]]; then
      component="${remaining%%/*}"
      remaining="${remaining#*/}"
    else
      component="$remaining"
      remaining=""
    fi
    case "$component" in
      ""|.) ;;
      ..) return 1 ;;
      *)
        current="${current%/}/$component"
        [[ ! -L "$current" ]] || return 1
        ;;
    esac
    [[ -n "$remaining" ]] || break
  done
}

evidence_outputs_are_safe() {
  local root="$1"
  local output
  for output in "$root/apexload-result.json" "$root/apexload-results.xml"; do
    [[ ! -L "$output" ]] || return 1
    if [[ -e "$output" && ! -f "$output" ]]; then
      return 1
    fi
  done
}

umask 077
requested_evidence_dir="${APEXLOAD_INPUT_EVIDENCE_DIRECTORY:-.apexload}"
evidence_path_error=false
if evidence_dir="$(prepare_evidence_dir "$requested_evidence_dir")" \
  && evidence_outputs_are_safe "$evidence_dir"; then
  :
else
  evidence_path_error=true
  fallback_evidence_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/apexload-evidence-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${RANDOM}-${RANDOM}"
  if evidence_dir="$(prepare_evidence_dir "$fallback_evidence_dir")" \
    && evidence_outputs_are_safe "$evidence_dir"; then
    :
  else
    printf 'apexload action: cannot create a safe fallback evidence directory\n' >&2
    exit 2
  fi
fi
json_path="$evidence_dir/apexload-result.json"
junit_path="$evidence_dir/apexload-results.xml"

if [[ "$evidence_path_error" == true ]]; then
  write_fallback configuration_error EVIDENCE_PATH_INVALID true || exit 2
  emit_output action-exit-code 2
  exit 0
fi

# Seed evidence and stable outputs before invoking the downloaded executable.
# A successful apexctl invocation overwrites the files and appends authoritative
# values; a startup crash leaves this valid schema-v1 fallback in place.
write_fallback execution_error NOT_STARTED false || exit 2

apexctl="${APEXLOAD_APEXCTL_BIN:-}"
install_step_outcome="${APEXLOAD_INSTALL_STEP_OUTCOME:-success}"
if [[ "$install_step_outcome" != success || ! -f "$apexctl" || ! -x "$apexctl" ]]; then
  failure_outcome="${APEXLOAD_INSTALL_FAILURE_OUTCOME:-execution_error}"
  write_fallback "$failure_outcome" INSTALL_FAILED true || exit 2
  emit_output action-exit-code 2
  exit 0
fi

mode="$(printf '%s' "${APEXLOAD_INPUT_EXECUTION_MODE:-controller}" | tr '[:upper:]' '[:lower:]')"
publish_check="${APEXLOAD_INPUT_PUBLISH_CHECK:-}"
if [[ -z "$publish_check" ]]; then
  if [[ "$mode" == local ]]; then
    publish_check=false
  else
    publish_check=true
  fi
fi

cmd=("$apexctl" ci run
  --json "$json_path"
  --junit "$junit_path"
  --github-output "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"
  --github-summary "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}"
  --execution-mode "$mode"
  --controller-url "${APEXLOAD_INPUT_CONTROLLER_URL:-}"
  --project-id "${APEXLOAD_INPUT_PROJECT_ID:-}"
  --script-path "${APEXLOAD_INPUT_SCRIPT_PATH:-}"
  --script-id "${APEXLOAD_INPUT_SCRIPT_ID:-}"
  --release-id "${APEXLOAD_INPUT_RELEASE_ID:-}"
  --test-name "${APEXLOAD_INPUT_TEST_NAME:-}"
  --auth-mode "${APEXLOAD_INPUT_AUTH_MODE:-oidc}"
  --vusers "${APEXLOAD_INPUT_VUSERS:-10}"
  --duration "${APEXLOAD_INPUT_DURATION:-1m}"
  --ramp-up "${APEXLOAD_INPUT_RAMP_UP:-0s}"
  --base-url "${APEXLOAD_INPUT_BASE_URL:-}"
  --variables-file "${APEXLOAD_INPUT_VARIABLES_FILE:-}"
  --require-sla="${APEXLOAD_INPUT_REQUIRE_SLA:-true}"
  --require-results="${APEXLOAD_INPUT_REQUIRE_RESULTS:-true}"
  --timeout "${APEXLOAD_INPUT_TIMEOUT:-30m}"
  --abort-on-cancel="${APEXLOAD_INPUT_ABORT_ON_CANCEL:-true}"
  --publish-check="$publish_check")

[[ -n "${APEXLOAD_INPUT_P95_LATENCY_MS:-}" ]] && cmd+=(--p95-latency-ms "$APEXLOAD_INPUT_P95_LATENCY_MS")
[[ -n "${APEXLOAD_INPUT_MAX_ERROR_PCT:-}" ]] && cmd+=(--max-error-pct "$APEXLOAD_INPUT_MAX_ERROR_PCT")
[[ -n "${APEXLOAD_INPUT_MAX_AVG_SERVER_PROCESSING_MS:-}" ]] && cmd+=(--max-avg-server-processing-ms "$APEXLOAD_INPUT_MAX_AVG_SERVER_PROCESSING_MS")
[[ -n "${APEXLOAD_INPUT_MAX_AVG_TRANSFER_MS:-}" ]] && cmd+=(--max-avg-transfer-ms "$APEXLOAD_INPUT_MAX_AVG_TRANSFER_MS")
[[ -n "${APEXLOAD_INPUT_MAX_ERROR_COUNT:-}" ]] && cmd+=(--max-error-count "$APEXLOAD_INPUT_MAX_ERROR_COUNT")
[[ -n "${APEXLOAD_INPUT_IDEMPOTENCY_KEY:-}" ]] && cmd+=(--idempotency-key "$APEXLOAD_INPUT_IDEMPOTENCY_KEY")
[[ -n "${APEXLOAD_INPUT_TELEMETRY_INCONCLUSIVE:-}" ]] && cmd+=(--telemetry-inconclusive "$APEXLOAD_INPUT_TELEMETRY_INCONCLUSIVE")
[[ -n "${APEXLOAD_INPUT_TELEMETRY_POLICY:-}" ]] && cmd+=(--telemetry-policy "$APEXLOAD_INPUT_TELEMETRY_POLICY")
[[ -n "${APEXLOAD_INPUT_WAIT_ANALYSIS:-}" ]] && cmd+=(--wait-analysis "$APEXLOAD_INPUT_WAIT_ANALYSIS")

"${cmd[@]}"
apexctl_exit=$?

if ! has_apexctl_completion_marker \
  || ! grep -Eq '"schema_version"[[:space:]]*:[[:space:]]*1' "$json_path" 2>/dev/null \
  || grep -Eq '"status"[[:space:]]*:[[:space:]]*"NOT_STARTED"' "$json_path" 2>/dev/null \
  || [[ ! -s "$junit_path" ]]; then
  write_fallback execution_error STARTUP_FAILED true || exit 2
  apexctl_exit=3
fi

emit_output action-exit-code "$apexctl_exit"
# Gate failure is intentionally deferred to the composite's final step so the
# action has one failure point after evidence and outputs have been finalized.
exit 0
