#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 OUTPUT.json OUTPUT.md" >&2
  echo "Both output paths are required; the script never chooses or overwrites a canonical evidence path." >&2
}

if [[ $# -ne 2 ]]; then
  usage
  exit 2
fi

output_json=$1
output_markdown=$2
if [[ -z "${output_json}" || -z "${output_markdown}" ]]; then
  usage
  exit 2
fi
if [[ -e "${output_json}" || -e "${output_markdown}" ]]; then
  echo "Refusing to overwrite an existing evidence artifact." >&2
  exit 1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)
mkdir -p -- "$(dirname -- "${output_json}")" "$(dirname -- "${output_markdown}")"

profile=${APEXLOAD_BENCHMARK_PROFILE:-release}
version=$(cd "${repo_root}" && ./scripts/resolve-version.sh 2>/dev/null || echo dev)
commit=$(cd "${repo_root}" && git rev-parse HEAD 2>/dev/null || true)
build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)
vcs_status=unknown
if [[ -n "${commit}" ]]; then
  if [[ -z "$(cd "${repo_root}" && git status --porcelain --untracked-files=normal 2>/dev/null)" ]]; then
    vcs_status=clean
  else
    vcs_status=modified
  fi
fi
buildinfo_package=github.com/jasonczech/apexload/pkg/buildinfo
ldflags="-X ${buildinfo_package}.Version=${version} -X ${buildinfo_package}.Commit=${commit} -X ${buildinfo_package}.BuildTime=${build_time}"

benchmark_args=(
  -profile "${profile}"
  -output "${output_json}"
  -markdown-output "${output_markdown}"
  -vcs-status "${vcs_status}"
)
if [[ -n "${APEXLOAD_BENCHMARK_HOST_CLASS:-}" ]]; then
  benchmark_args+=(-host-class "${APEXLOAD_BENCHMARK_HOST_CLASS}")
fi
if [[ -n "${APEXLOAD_BENCHMARK_RUNNER_IMAGE:-}" ]]; then
  benchmark_args+=(-runner-image "${APEXLOAD_BENCHMARK_RUNNER_IMAGE}")
fi

cd "${repo_root}"
go run -trimpath -ldflags "${ldflags}" ./cmd/apexbench "${benchmark_args[@]}"
go run -trimpath -ldflags "${ldflags}" ./cmd/apexbench -validate "${output_json}"

echo "Wrote validated component evidence:" >&2
echo "  JSON: ${output_json}" >&2
echo "  Markdown: ${output_markdown}" >&2
