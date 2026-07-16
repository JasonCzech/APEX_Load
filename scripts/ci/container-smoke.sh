#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "${1// }" ]]; then
  echo "usage: $0 <controller-image>" >&2
  exit 2
fi

image=$1
port=${APEXLOAD_SMOKE_PORT:-18080}
expected_revision=0000000000000000000000000000000000000000
expected_commit=${expected_revision:0:7}
expected_version=0.0.0-ci-smoke
expected_build_time=1970-01-01T00:00:00Z
name="apexload-ci-smoke-${RANDOM}-$$"
workdir=$(mktemp -d)

cleanup() {
  exit_code=$?
  trap - EXIT INT TERM
  if docker inspect "${name}" >/dev/null 2>&1; then
    if (( exit_code != 0 )); then
      docker logs "${name}" >&2 || true
    fi
    docker rm --force "${name}" >/dev/null 2>&1 || true
  fi
  rm -rf "${workdir}"
  exit "${exit_code}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run --detach \
  --name "${name}" \
  --publish "127.0.0.1:${port}:8080" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777 \
  --tmpfs /data:rw,nosuid,nodev,size=256m,uid=65532,gid=65532,mode=0750 \
  --env APEXLOAD_DB=/data/apexload.db \
  --env APEXLOAD_ARCHIVE_DIR=/data/archives \
  "${image}" >/dev/null

base_url="http://127.0.0.1:${port}"
for _ in $(seq 1 60); do
  if curl --silent --show-error --fail "${base_url}/api/v1/system/health" \
    --output "${workdir}/health.json"; then
    break
  fi
  sleep 1
done

if [[ ! -s "${workdir}/health.json" ]]; then
  echo "controller did not become healthy within 60 seconds" >&2
  exit 1
fi

curl --silent --show-error --fail "${base_url}/api/v1/system/readiness" \
  --output "${workdir}/readiness.json"
curl --silent --show-error --fail "${base_url}/api/v1/system/version" \
  --output "${workdir}/version.json"
curl --silent --show-error --fail "${base_url}/" \
  --output "${workdir}/index.html"

jq -e '.status == "ok" or .status == "healthy"' "${workdir}/health.json" >/dev/null
jq -e '.status == "ready" or .status == "degraded"' "${workdir}/readiness.json" >/dev/null
jq -e --arg revision "${expected_revision}" --arg commit "${expected_commit}" \
  --arg version "${expected_version}" \
  --arg build_time "${expected_build_time}" \
  '.version == $version and .revision == $revision and .commit == $commit and .build_time == $build_time' \
  "${workdir}/version.json" >/dev/null

python3 - "${base_url}" "${workdir}/index.html" "${expected_version}" "${expected_build_time}" <<'PY'
import datetime
import re
import sys
import urllib.parse
import urllib.request
from collections import deque
from html.parser import HTMLParser


class AssetParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.assets = set()

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == "script" and values.get("src"):
            self.assets.add(values["src"])
        if tag == "link" and values.get("href"):
            self.assets.add(values["href"])


base_url, index_path, expected_version, expected_build_time = sys.argv[1:]
parser = AssetParser()
with open(index_path, encoding="utf-8") as index:
    parser.feed(index.read())

if not parser.assets:
    raise SystemExit("embedded dashboard index does not reference any assets")

base_host = urllib.parse.urlparse(base_url).netloc
expected_dashboard_build_time = (
    datetime.datetime.fromisoformat(expected_build_time.replace("Z", "+00:00"))
    .isoformat(timespec="milliseconds")
    .replace("+00:00", "Z")
    .encode()
)
dashboard_build_time_seen = False
dashboard_version_seen = False
asset_reference = re.compile(
    rb"(?:/assets/|\./)[A-Za-z0-9_.-]+\.(?:css|gif|jpe?g|js|png|svg|webp|woff2?)"
)
pending = deque(urllib.parse.urljoin(base_url + "/", asset) for asset in sorted(parser.assets))
visited = set()
while pending:
    url = pending.popleft()
    if url in visited:
        continue
    visited.add(url)
    if urllib.parse.urlparse(url).netloc != base_host:
        continue
    with urllib.request.urlopen(url, timeout=10) as response:
        content = response.read()
        if response.status != 200 or not content:
            raise SystemExit(f"dashboard asset is unavailable: {url}")
        if expected_dashboard_build_time in content:
            dashboard_build_time_seen = True
        if expected_version.encode() in content:
            dashboard_version_seen = True
        for reference in asset_reference.findall(content):
            pending.append(urllib.parse.urljoin(url, reference.decode("ascii")))

if not dashboard_build_time_seen:
    raise SystemExit("embedded dashboard does not contain the deterministic release build time")
if not dashboard_version_seen:
    raise SystemExit("embedded dashboard does not contain the deterministic release version")
PY

echo "controller image passed health, readiness, build-metadata, and deterministic dashboard asset checks"
