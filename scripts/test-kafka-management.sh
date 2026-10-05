#!/usr/bin/env bash
# Disposable real Apache Kafka fixture. Uses the session's Go cache/settings.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
for argument in "$@"; do
  case "$argument" in
    --suite|--suite=*) echo 'This wrapper only qualifies management; use the separate owned-seek wrapper for execution.' >&2; exit 2 ;;
  esac
done
exec python3 scripts/kafka-management-fixture.py --suite management "$@"
