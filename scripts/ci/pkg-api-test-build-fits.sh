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
# That single compile holds 6-9.5 GB (measured with go1.26.9 on darwin/arm64,
# with and without -tags analytics_duckdb). About a hundred test stores promote
# storage.Store's ~900 methods, so the compiler builds over 200,000 wrapper
# functions and keeps their code until it writes the object file. Hosted arm64
# macOS runners have 7 GB: there the compile swaps for hours and the job hits
# its timeout. Those legs leave pkg/api to the macOS Intel (14 GB), Linux and
# Windows (16 GB) legs.
set -euo pipefail

required=$((12 << 30))
case "$(uname -s)" in
  Darwin) total=$(sysctl -n hw.memsize) ;;
  # Hosted Linux and Windows runners have 16 GB for public repositories (8 GB
  # for private ones, where the compile fails loudly rather than skipping).
  *) exit 0 ;;
esac
if ((total < required)); then
  echo "::notice::Skipping pkg/api tests: its test binary needs about 10 GB of memory to compile and this runner has $((total >> 30)) GB. The desktop workflow's macOS Intel, Linux and Windows legs run them."
  exit 3
fi
