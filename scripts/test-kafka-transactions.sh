#!/usr/bin/env bash
# Fresh-source Kafka LSO/atomic transaction/fencing proof; no missing cases pass.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
for argument in "$@"; do
  case "$argument" in
    --suite|--suite=*) echo 'This wrapper only qualifies fresh-source Kafka transactions; use the separate wrappers for other suites.' >&2; exit 2 ;;
  esac
done
exec python3 scripts/kafka-management-fixture.py --suite transactions --port 19402 "$@"
