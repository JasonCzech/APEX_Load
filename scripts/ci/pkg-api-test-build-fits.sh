#!/usr/bin/env bash
# Exits 0 when this runner has the memory to compile pkg/api's test binary and
# 3 when it does not; any other status is an error the caller must not read as
# a skip:
#
#   if scripts/ci/pkg-api-test-build-fits.sh; then
#     go test ./pkg/api ...
#   elif (($? != 3)); then
#     exit 1
#   fi
#
# That single compile peaks near 6 GB (measured with go1.26.9 on darwin/arm64,
# with and without -tags analytics_duckdb, at three CPUs). It peaked at 8-9.5 GB
# while the package's test stores embedded the store they wrapped: see
# pkg/api/store_overlay_doc_test.go. Hosted arm64 macOS runners have 7 GB in
# all, shared with the system and the runner; there the old compile swapped for
# hours until the job's timeout, and the new one is unproven. Those legs leave
# pkg/api to the Linux and Windows (16 GB) legs.
set -euo pipefail

required=$((10 << 30))
case "$(uname -s)" in
  Darwin) total=$(sysctl -n hw.memsize) ;;
  # Hosted Linux and Windows runners have 16 GB for public repositories (8 GB
  # for private ones, where the compile fails loudly rather than skipping).
  *) exit 0 ;;
esac
if ((total < required)); then
  echo "::notice::Skipping pkg/api tests: compiling its test binary peaks near 6 GB, too much for this runner's $((total >> 30)) GB. The desktop workflow's Linux and Windows legs run them."
  exit 3
fi
