#!/usr/bin/env bash
# Real recording -> authoring -> replay. See docs/qa/RECORDING_E2E.md.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
if [[ "${1:-}" == "--help" ]]; then
  echo 'Usage: scripts/run-recording-e2e.sh [spec-file] [--grep pattern] [--grep-invert pattern]'
  echo 'APEXLOAD_E2E_PROFILE=dev-admin|local-auth selects one profile; default: both.'
  echo 'Requires pinned Node, Go, Chromium runtimes, and a display (xvfb-run on Linux).'
  exit 0
fi
node dashboard/scripts/check-node-version.mjs
case "${APEXLOAD_E2E_PROFILE:-}" in ''|dev-admin|local-auth) ;; *) echo 'Unknown authentication profile' >&2; exit 2;; esac
if [[ "$(uname -s)" == Linux && -z "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]]; then
  command -v xvfb-run >/dev/null || { echo 'Install Xvfb for headed recording tests.' >&2; exit 1; }
  exec xvfb-run -a "$0" "$@"
fi
exec node scripts/run-recording-e2e.mjs "$@"
