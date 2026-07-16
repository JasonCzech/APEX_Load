#!/usr/bin/env bash
set -euo pipefail

action_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

output_value() {
  local key="$1"
  local file="$2"
  awk -v wanted="$key" '
    collecting && $0 == delimiter {
      result = value
      collecting = 0
      next
    }
    !collecting && index($0, wanted "<<") == 1 {
      delimiter = substr($0, length(wanted) + 3)
      value = ""
      first = 1
      collecting = 1
      next
    }
    !collecting && index($0, wanted "=") == 1 {
      result = substr($0, length(wanted) + 2)
      next
    }
    collecting {
      if (first) {
        value = $0
        first = 0
      } else {
        value = value ORS $0
      }
    }
    END { printf "%s", result }
  ' "$file"
}

assert_output() {
  local key="$1"
  local want="$2"
  local file="$3"
  local got
  got="$(output_value "$key" "$file")"
  [[ "$got" == "$want" ]] || {
    printf 'output %s = %q, want %q\n' "$key" "$got" "$want" >&2
    exit 1
  }
}

fake="$tmp/"$'apexctl 100%\nEOF-shaped-value'
cat > "$fake" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == version ]]; then
  echo "apexctl v9.9.9"
  exit 0
fi
printf '%s\n' "$@" > "$CAPTURE_ARGS"
json=""
junit=""
output=""
summary=""
while (($#)); do
  case "$1" in
    --json) json="$2"; shift 2 ;;
    --junit) junit="$2"; shift 2 ;;
    --github-output) output="$2"; shift 2 ;;
    --github-summary) summary="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '{"schema_version":1,"outcome":"passed","status":"COMPLETE"}\n' > "$json"
printf '<testsuites/>\n' > "$junit"
printf '%s\n' \
  'outcome<<FAKE_OUTCOME' 'passed' 'FAKE_OUTCOME' \
  'status<<FAKE_STATUS' 'COMPLETE' 'FAKE_STATUS' \
  "json-path<<FAKE_JSON" "$json" 'FAKE_JSON' \
  "junit-path<<FAKE_JUNIT" "$junit" 'FAKE_JUNIT' \
  'apexload-evidence-complete<<FAKE_COMPLETE' 'true' 'FAKE_COMPLETE' >> "$output"
printf '## passed\n' >> "$summary"
FAKE
chmod 0755 "$fake"

# A trusted preinstalled binary supports spaces, percent characters, and
# newlines in its path without legacy percent escaping or command injection.
install_output="$tmp/install-output"
GITHUB_OUTPUT="$install_output" \
APEXLOAD_INPUT_APEXCTL_PATH="$fake" \
APEXLOAD_INPUT_APEXCTL_VERSION="" \
"$action_dir/install.sh"
fake_resolved="$(cd "$(dirname "$fake")" && pwd -P)/$(basename "$fake")"
assert_output path "$fake_resolved" "$install_output"
assert_output version v9.9.9 "$install_output"

# A bad explicit path is a typed configuration failure.
bad_path_output="$tmp/bad-path-output"
if GITHUB_OUTPUT="$bad_path_output" \
  APEXLOAD_INPUT_APEXCTL_PATH="$tmp/not-an-executable" \
  "$action_dir/install.sh"; then
  echo "bad apexctl path unexpectedly passed" >&2
  exit 1
fi
assert_output failure-outcome configuration_error "$bad_path_output"

# Network/download failures are typed execution failures without trusting a
# partial asset.
download_bin="$tmp/download-bin"
mkdir -p "$download_bin"
cat > "$download_bin/curl" <<'CURL'
#!/usr/bin/env bash
exit 22
CURL
chmod 0755 "$download_bin/curl"
download_output="$tmp/download-output"
if PATH="$download_bin:$PATH" \
  GITHUB_ACTION_PATH="$action_dir" \
  GITHUB_OUTPUT="$download_output" \
  RUNNER_OS=Linux RUNNER_ARCH=X64 RUNNER_TEMP="$tmp" \
  APEXLOAD_INPUT_APEXCTL_PATH="" \
  APEXLOAD_INPUT_APEXCTL_VERSION=v1.2.3 \
  APEXLOAD_INPUT_RELEASE_BASE_URL=https://releases.example.invalid \
  APEXLOAD_INPUT_RELEASE_PUBLIC_KEY="$tmp/key.pem" \
  "$action_dir/install.sh"; then
  echo "failed download unexpectedly passed" >&2
  exit 1
fi
assert_output failure-outcome execution_error "$download_output"

# A failed detached signature always fails closed before checksum or execution.
signature_bin="$tmp/signature-bin"
mkdir -p "$signature_bin"
cat > "$signature_bin/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
output=""
while (($#)); do
  if [[ "$1" == --output ]]; then
    output="$2"
    break
  fi
  shift
done
[[ -n "$output" ]]
printf 'untrusted-content\n' > "$output"
CURL
cat > "$signature_bin/openssl" <<'OPENSSL'
#!/usr/bin/env bash
exit 1
OPENSSL
chmod 0755 "$signature_bin/curl" "$signature_bin/openssl"
printf '%s\n' 'not-a-real-public-key' > "$tmp/key.pem"
signature_output="$tmp/signature-output"
if PATH="$signature_bin:$PATH" \
  GITHUB_ACTION_PATH="$action_dir" \
  GITHUB_OUTPUT="$signature_output" \
  RUNNER_OS=Linux RUNNER_ARCH=X64 RUNNER_TEMP="$tmp" \
  APEXLOAD_INPUT_APEXCTL_PATH="" \
  APEXLOAD_INPUT_APEXCTL_VERSION=v1.2.3 \
  APEXLOAD_INPUT_RELEASE_BASE_URL=https://releases.example.invalid \
  APEXLOAD_INPUT_RELEASE_PUBLIC_KEY="$tmp/key.pem" \
  "$action_dir/install.sh"; then
  echo "invalid signature unexpectedly passed" >&2
  exit 1
fi
assert_output failure-outcome execution_error "$signature_output"

# The signed manifest must bind the exact platform asset, version, and digest.
manifest_checksum="$(printf 'a%.0s' {1..64})"
manifest_file="$tmp/release-manifest.json"
cat > "$manifest_file" <<EOF
{
  "schema_version": 1,
  "version": "1.2.3",
  "tag": "v1.2.3",
  "canonical_apexctl_version": "v1.2.3",
  "binaries": [
    {"name":"apexctl","os":"linux","architecture":"amd64","asset":"apexctl_1.2.3_linux_amd64","sha256":"$manifest_checksum"}
  ],
  "assets": [
    {"asset":"apexctl_1.2.3_linux_amd64","sha256":"$manifest_checksum","size":42}
  ]
}
EOF
node "$action_dir/verify-manifest.js" "$manifest_file" v1.2.3 linux amd64 \
  apexctl_1.2.3_linux_amd64 "$manifest_checksum"
if node "$action_dir/verify-manifest.js" "$manifest_file" v1.2.4 linux amd64 \
  apexctl_1.2.3_linux_amd64 "$manifest_checksum"; then
  echo "manifest with the wrong release tag unexpectedly passed" >&2
  exit 1
fi
node -e '
  const fs=require("fs"); const p=process.argv[1]; const m=JSON.parse(fs.readFileSync(p));
  m.binaries.push({...m.binaries[0]}); fs.writeFileSync(p, JSON.stringify(m));
' "$manifest_file"
if node "$action_dir/verify-manifest.js" "$manifest_file" v1.2.3 linux amd64 \
  apexctl_1.2.3_linux_amd64 "$manifest_checksum"; then
  echo "manifest with duplicate platform records unexpectedly passed" >&2
  exit 1
fi

# Normal execution preserves array boundaries for paths with spaces and
# shell-injection-shaped inputs.
sentinel="$tmp/should-not-exist"
capture="$tmp/args"
run_output="$tmp/run-output"
summary="$tmp/summary"
evidence="$tmp/evidence with spaces"
script="$tmp/script with spaces.json"
printf '{}\n' > "$script"

CAPTURE_ARGS="$capture" \
GITHUB_OUTPUT="$run_output" \
GITHUB_STEP_SUMMARY="$summary" \
RUNNER_TEMP="$tmp" \
APEXLOAD_APEXCTL_BIN="$fake" \
APEXLOAD_INSTALL_STEP_OUTCOME=success \
APEXLOAD_INPUT_EXECUTION_MODE=local \
APEXLOAD_INPUT_SCRIPT_PATH="$script" \
APEXLOAD_INPUT_BASE_URL="\$(touch $sentinel)" \
APEXLOAD_INPUT_PUBLISH_CHECK="" \
APEXLOAD_INPUT_REQUIRE_SLA=false \
APEXLOAD_INPUT_REQUIRE_RESULTS=false \
APEXLOAD_INPUT_EVIDENCE_DIRECTORY="$evidence" \
"$action_dir/run.sh"

[[ ! -e "$sentinel" ]] || { echo "injection-shaped input executed" >&2; exit 1; }
grep -Fx "$script" "$capture" >/dev/null
grep -Fx "\$(touch $sentinel)" "$capture" >/dev/null
grep -Fx false "$capture" >/dev/null
test -f "$evidence/apexload-result.json"
test -f "$evidence/apexload-results.xml"
assert_output outcome passed "$run_output"
assert_output action-exit-code 0 "$run_output"

# Every terminal gate outcome must retain valid JSON/JUnit evidence and defer
# its single success/failure decision to conclude.sh. This exercises the
# composite action boundary independently from apexctl's Go unit tests.
outcome_fake="$tmp/outcome-apexctl"
cat > "$outcome_fake" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
json=""
junit=""
output=""
summary=""
while (($#)); do
  case "$1" in
    --json) json="$2"; shift 2 ;;
    --junit) junit="$2"; shift 2 ;;
    --github-output) output="$2"; shift 2 ;;
    --github-summary) summary="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n "$json" && -n "$junit" && -n "$output" && -n "$summary" ]]

printf '{"schema_version":1,"outcome":"%s","test_id":"matrix-test","status":"%s","sla_evaluated":%s,"sla_breached":%s,"transactions":%s,"errors":0,"tps":1.5,"duration_ms":42}\n' \
  "$FAKE_OUTCOME" "$FAKE_STATUS" "$FAKE_SLA_EVALUATED" "$FAKE_SLA_BREACHED" "$FAKE_TRANSACTIONS" > "$json"
if [[ "$FAKE_EXIT" == 0 ]]; then
  printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' '<testsuites tests="1" failures="0"><testsuite name="apexload:ci" tests="1" failures="0"><testcase name="performance-gate"/></testsuite></testsuites>' > "$junit"
else
  printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' '<testsuites tests="1" failures="1"><testsuite name="apexload:ci" tests="1" failures="1"><testcase name="performance-gate"><failure message="expected gate failure"/></testcase></testsuite></testsuites>' > "$junit"
fi

emit() {
  local key="$1"
  local value="$2"
  local delimiter="FAKE_MATRIX_${key}_$$"
  printf '%s<<%s\n%s\n%s\n' "$key" "$delimiter" "$value" "$delimiter" >> "$output"
}
emit outcome "$FAKE_OUTCOME"
emit test-id matrix-test
emit status "$FAKE_STATUS"
emit sla-evaluated "$FAKE_SLA_EVALUATED"
emit sla-breached "$FAKE_SLA_BREACHED"
emit transactions "$FAKE_TRANSACTIONS"
emit errors 0
emit tps 1.5
emit duration-ms 42
emit report-url https://apexload.example/reports/matrix-test
emit check-url https://github.example/checks/matrix-test
emit json-path "$json"
emit junit-path "$junit"
emit apexload-evidence-complete true
printf '## APEX Load performance gate: %s\n' "$FAKE_OUTCOME" >> "$summary"
exit "$FAKE_EXIT"
FAKE
chmod 0755 "$outcome_fake"

while IFS='|' read -r case_name outcome status sla_evaluated sla_breached transactions gate_exit; do
  case_output="$tmp/matrix-${case_name}-output"
  case_summary="$tmp/matrix-${case_name}-summary"
  case_evidence="$tmp/matrix-${case_name}-evidence"
  GITHUB_OUTPUT="$case_output" \
  GITHUB_STEP_SUMMARY="$case_summary" \
  RUNNER_TEMP="$tmp" \
  APEXLOAD_APEXCTL_BIN="$outcome_fake" \
  APEXLOAD_INSTALL_STEP_OUTCOME=success \
  APEXLOAD_INPUT_EXECUTION_MODE=controller \
  APEXLOAD_INPUT_EVIDENCE_DIRECTORY="$case_evidence" \
  FAKE_OUTCOME="$outcome" \
  FAKE_STATUS="$status" \
  FAKE_SLA_EVALUATED="$sla_evaluated" \
  FAKE_SLA_BREACHED="$sla_breached" \
  FAKE_TRANSACTIONS="$transactions" \
  FAKE_EXIT="$gate_exit" \
  "$action_dir/run.sh"

  assert_output outcome "$outcome" "$case_output"
  assert_output status "$status" "$case_output"
  assert_output sla-evaluated "$sla_evaluated" "$case_output"
  assert_output sla-breached "$sla_breached" "$case_output"
  assert_output action-exit-code "$gate_exit" "$case_output"
  case_json="$(output_value json-path "$case_output")"
  case_junit="$(output_value junit-path "$case_output")"
  node -e '
    const fs = require("fs");
    const result = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
    if (result.schema_version !== 1 || result.outcome !== process.argv[2] || result.status !== process.argv[3]) process.exit(1);
  ' "$case_json" "$outcome" "$status"
  python3 -c 'import sys, xml.etree.ElementTree as ET; ET.parse(sys.argv[1])' "$case_junit"
  grep -F "$outcome" "$case_summary" >/dev/null

  if [[ "$gate_exit" == 0 ]]; then
    APEXLOAD_INSTALL_STEP_OUTCOME=success APEXLOAD_RUN_STEP_OUTCOME=success \
      APEXLOAD_GATE_EXIT_CODE="$gate_exit" "$action_dir/conclude.sh" >/dev/null
  elif APEXLOAD_INSTALL_STEP_OUTCOME=success APEXLOAD_RUN_STEP_OUTCOME=success \
    APEXLOAD_GATE_EXIT_CODE="$gate_exit" "$action_dir/conclude.sh"; then
    printf 'terminal case %s unexpectedly concluded successfully\n' "$case_name" >&2
    exit 1
  fi
done <<'CASES'
passed|passed|COMPLETE|true|false|12|0
sla-breach|sla_breached|COMPLETE|true|true|12|1
missing-sla|configuration_error|MISSING_SLA|false|false|12|2
no-results|execution_error|NO_RESULTS|true|false|0|3
timeout|timed_out|TIMED_OUT|true|false|5|3
abort|cancelled|ABORTED|true|false|5|4
CASES

# If installation fails, run.sh still creates valid schema-v1 JSON/JUnit,
# summary, and every stable output before the final action failure.
install_failure_output="$tmp/install-failure-output"
install_failure_summary="$tmp/install-failure-summary"
install_failure_evidence="$tmp/install-failure-evidence"
GITHUB_OUTPUT="$install_failure_output" \
GITHUB_STEP_SUMMARY="$install_failure_summary" \
RUNNER_TEMP="$tmp" \
APEXLOAD_APEXCTL_BIN="" \
APEXLOAD_INSTALL_STEP_OUTCOME=failure \
APEXLOAD_INSTALL_FAILURE_OUTCOME=configuration_error \
APEXLOAD_INPUT_EVIDENCE_DIRECTORY="$install_failure_evidence" \
"$action_dir/run.sh"
assert_output outcome configuration_error "$install_failure_output"
assert_output status INSTALL_FAILED "$install_failure_output"
assert_output action-exit-code 2 "$install_failure_output"
grep -Eq '"schema_version"[[:space:]]*:[[:space:]]*1' "$install_failure_evidence/apexload-result.json"
grep -F '<testsuites' "$install_failure_evidence/apexload-results.xml" >/dev/null
grep -F 'APEX Load performance gate' "$install_failure_summary" >/dev/null

# A verified binary that exits before writing evidence is converted into a
# stable startup failure rather than bypassing the final gate.
startup_fake="$tmp/startup-failure-apexctl"
cat > "$startup_fake" <<'FAKE'
#!/usr/bin/env bash
exit 7
FAKE
chmod 0755 "$startup_fake"
startup_output="$tmp/startup-output"
startup_summary="$tmp/startup-summary"
startup_evidence="$tmp/startup-evidence"
GITHUB_OUTPUT="$startup_output" \
GITHUB_STEP_SUMMARY="$startup_summary" \
RUNNER_TEMP="$tmp" \
APEXLOAD_APEXCTL_BIN="$startup_fake" \
APEXLOAD_INSTALL_STEP_OUTCOME=success \
APEXLOAD_INPUT_EXECUTION_MODE=local \
APEXLOAD_INPUT_SCRIPT_PATH="$script" \
APEXLOAD_INPUT_PUBLISH_CHECK=false \
APEXLOAD_INPUT_EVIDENCE_DIRECTORY="$startup_evidence" \
"$action_dir/run.sh"
assert_output outcome execution_error "$startup_output"
assert_output status STARTUP_FAILED "$startup_output"
assert_output action-exit-code 3 "$startup_output"
grep -Eq '"schema_version"[[:space:]]*:[[:space:]]*1' "$startup_evidence/apexload-result.json"
grep -F '<testsuites' "$startup_evidence/apexload-results.xml" >/dev/null

# An unwritable/invalid requested evidence directory falls back to RUNNER_TEMP,
# retains evidence, and fails the gate as configuration_error.
bad_evidence="$tmp/evidence-is-a-file"
printf 'not a directory\n' > "$bad_evidence"
bad_evidence_output="$tmp/bad-evidence-output"
bad_evidence_summary="$tmp/bad-evidence-summary"
GITHUB_OUTPUT="$bad_evidence_output" \
GITHUB_STEP_SUMMARY="$bad_evidence_summary" \
RUNNER_TEMP="$tmp" \
APEXLOAD_APEXCTL_BIN="$fake" \
APEXLOAD_INSTALL_STEP_OUTCOME=success \
APEXLOAD_INPUT_EVIDENCE_DIRECTORY="$bad_evidence" \
"$action_dir/run.sh"
assert_output outcome configuration_error "$bad_evidence_output"
assert_output status EVIDENCE_PATH_INVALID "$bad_evidence_output"
bad_json="$(output_value json-path "$bad_evidence_output")"
bad_junit="$(output_value junit-path "$bad_evidence_output")"
test -f "$bad_json"
test -f "$bad_junit"

# Gate failure occurs only in the final conclusion helper.
APEXLOAD_INSTALL_STEP_OUTCOME=success \
APEXLOAD_RUN_STEP_OUTCOME=success \
APEXLOAD_GATE_EXIT_CODE=0 \
"$action_dir/conclude.sh" >/dev/null
if APEXLOAD_INSTALL_STEP_OUTCOME=success \
  APEXLOAD_RUN_STEP_OUTCOME=success \
  APEXLOAD_GATE_EXIT_CODE=3 \
  "$action_dir/conclude.sh"; then
  echo "failed gate unexpectedly concluded successfully" >&2
  exit 1
fi

echo "composite action helper tests passed"
