#!/usr/bin/env bash
# Genuine owned-partition seek qualification; missing fixture cases cannot pass.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
for argument in "$@"; do
  case "$argument" in
    --suite|--suite=*) echo 'This wrapper only qualifies owned-partition seek; use the separate management wrapper for management.' >&2; exit 2 ;;
  esac
done
exec python3 scripts/kafka-management-fixture.py --suite owned-seek "$@"
