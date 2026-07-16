#!/usr/bin/env bash
set -euo pipefail

install_outcome="${APEXLOAD_INSTALL_STEP_OUTCOME:-failure}"
run_outcome="${APEXLOAD_RUN_STEP_OUTCOME:-failure}"
gate_exit="${APEXLOAD_GATE_EXIT_CODE:-}"

if [[ "$install_outcome" != success ]]; then
  printf 'apexload action: verified apexctl installation failed; see the install step and retained evidence\n' >&2
  exit 1
fi
if [[ "$run_outcome" != success ]]; then
  printf 'apexload action: evidence finalization failed; see the run step\n' >&2
  exit 1
fi
if [[ ! "$gate_exit" =~ ^[0-9]+$ ]]; then
  printf 'apexload action: the gate did not produce a valid final exit code\n' >&2
  exit 1
fi
if [[ "$gate_exit" != 0 ]]; then
  printf 'apexload action: performance gate failed with apexctl exit code %s\n' "$gate_exit" >&2
  exit 1
fi

printf 'APEX Load performance gate passed.\n'
