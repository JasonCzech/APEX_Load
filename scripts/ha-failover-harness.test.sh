#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d -t apexload-ha-harness-test.XXXXXX)"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/stubs"
cat > "$test_dir/stubs/go" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$test_dir/stubs/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$HARNESS_DOCKER_LOG"
exit 125
EOF
chmod +x "$test_dir/stubs/go" "$test_dir/stubs/docker"
status=0
PATH="$test_dir/stubs:$PATH" HARNESS_DOCKER_LOG="$test_dir/docker.log" HARNESS_DIR="$test_dir/run" \
  bash "$repo_root/scripts/ha-failover-harness.sh" > "$test_dir/output.log" 2>&1 || status=$?
if [[ "$status" != 1 ]]; then
  cat "$test_dir/output.log"
  echo "expected an immediate startup failure, got $status" >&2
  exit 1
fi
if [[ "$(wc -l < "$test_dir/docker.log" | tr -d ' ')" != 1 ]] || ! grep -q '^run .*--name apexload-ha-' "$test_dir/docker.log"; then
  cat "$test_dir/docker.log"
  echo "harness touched Docker resources after a failed startup" >&2
  exit 1
fi
echo "HA harness startup cleanup regression passed"
