#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release artifact build: %s\n' "$*" >&2
  exit 1
}

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$root_dir/scripts/release/semver.sh"
output_dir="${1:-}"
version="${2:-}"
commit="${3:-}"
build_time="${4:-}"

[[ -n "$output_dir" ]] || die "output directory is required"
is_apex_semver "$version" || die "version must be canonical semantic version without the v prefix"
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || die "commit must be a full lowercase SHA"
[[ "$build_time" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] \
  || die "build time must be UTC RFC3339 without fractional seconds"

mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd -P)"
bi=github.com/jasonczech/apexload/pkg/buildinfo
ldflags="-s -w -X ${bi}.Version=${version} -X ${bi}.Commit=${commit} -X ${bi}.BuildTime=${build_time}"

build_go() {
  local command="$1"
  local goos="$2"
  local goarch="$3"
  local output="$4"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
    -ldflags "$ldflags" -o "$output" "./cmd/${command}"
}

cd "$root_dir"
if [[ "${APEXLOAD_BUILD_APEXCTL:-1}" == 1 ]]; then
  while read -r goos goarch; do
    suffix=""
    [[ "$goos" == windows ]] && suffix=.exe
    asset="apexctl_${version}_${goos}_${goarch}${suffix}"
    build_go apexctl "$goos" "$goarch" "$output_dir/$asset"
  done <<'EOF'
linux amd64
linux arm64
darwin arm64
windows amd64
EOF
fi

for goarch in amd64 arm64; do
  package_dir="$(mktemp -d)"
  for command in controller generator apexload-record-agent apexload-collector-agent; do
    build_go "$command" linux "$goarch" "$package_dir/$command"
  done
  tar -C "$package_dir" -czf "$output_dir/apexload-server_${version}_linux_${goarch}.tar.gz" .
  rm -rf "$package_dir"
done

appservice_dir="$(mktemp -d)"
mkdir -p "$appservice_dir/bin"
build_go controller linux amd64 "$appservice_dir/bin/controller"
install -m 0644 deployments/azure/appservice/package.json "$appservice_dir/package.json"
install -m 0644 deployments/azure/appservice/launcher.js "$appservice_dir/launcher.js"
(
  cd "$appservice_dir"
  zip -q -r "$output_dir/apexload-appservice_${version}_linux_amd64.zip" .
)
rm -rf "$appservice_dir"

helm package deployments/helm/apexload \
  --version "$version" \
  --app-version "$version" \
  --destination "$output_dir" >/dev/null

if compgen -G "$output_dir/apexctl_*" >/dev/null; then
  chmod 0755 "$output_dir"/apexctl_*
fi

printf 'built immutable release assets in %s\n' "$output_dir"
