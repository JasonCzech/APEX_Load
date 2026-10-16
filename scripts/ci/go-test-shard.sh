#!/usr/bin/env bash
# Run one deterministic shard of a Go package's tests.
#
#   scripts/ci/go-test-shard.sh <package> <shard-index> <shard-count> [go test flags...]
#
# Top-level tests, examples and fuzz targets are assigned to a shard by a stable
# hash of their names, so each runs in exactly one shard and a new test lands
# in a predictable one. A shard's -run pattern is split into batches that stay
# well under Linux's 128 KiB limit for a single argument; each batch reuses the
# cached test binary.
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <package> <shard-index> <shard-count> [go test flags...]" >&2
  exit 2
fi
package=$1
shard=$2
shards=$3
shift 3
if ! [[ "${shard}" =~ ^[0-9]+$ && "${shards}" =~ ^[1-9][0-9]*$ ]] || ((shard >= shards)); then
  echo "shard index must be an integer in [0, shard-count)" >&2
  exit 2
fi

# A build failure must fail the shard, not read as an empty one.
listing=$(go test -list '.*' "$@" "${package}")
names=$(grep -E '^(Test|Example|Fuzz)[[:alnum:]_]*$' <<<"${listing}" || true)
# A substitution, not a process substitution, so a failed partition fails the shard.
partition=$(python3 -c '
import sys, zlib
shard, shards = int(sys.argv[1]), int(sys.argv[2])
batch, size = [], 0
for name in sys.stdin.read().split():
    if zlib.crc32(name.encode()) % shards != shard:
        continue
    if batch and size + len(name) + 1 > 60000:
        print("|".join(batch))
        batch, size = [], 0
    batch.append(name)
    size += len(name) + 1
if batch:
    print("|".join(batch))
' "${shard}" "${shards}" <<<"${names}")
patterns=()
while IFS= read -r pattern; do
  [[ -n "${pattern}" ]] && patterns+=("${pattern}")
done <<<"${partition}"

if ((${#patterns[@]} == 0)); then
  echo "shard ${shard}/${shards} of ${package} has no tests"
  exit 0
fi
status=0
for pattern in "${patterns[@]}"; do
  go test "$@" -run "^(${pattern})\$" "${package}" || status=1
done
exit "${status}"
