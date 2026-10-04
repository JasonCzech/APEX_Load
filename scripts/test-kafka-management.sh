#!/usr/bin/env bash
# Disposable real Apache Kafka fixture. Uses the session's Go cache/settings.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
exec python3 scripts/kafka-management-fixture.py "$@"
