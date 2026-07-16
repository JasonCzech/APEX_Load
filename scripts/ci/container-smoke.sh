#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "${1// }" ]]; then
  echo "usage: $0 <controller-image>" >&2
  exit 2
fi

image=$1
port=${APEXLOAD_SMOKE_PORT:-18080}
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
jq -e '.version == "ci" and .commit == "0000000000000000000000000000000000000000"' \
  "${workdir}/version.json" >/dev/null

python3 - "${base_url}" "${workdir}/index.html" <<'PY'
import sys
import urllib.parse
import urllib.request
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


base_url, index_path = sys.argv[1:]
parser = AssetParser()
with open(index_path, encoding="utf-8") as index:
    parser.feed(index.read())

if not parser.assets:
    raise SystemExit("embedded dashboard index does not reference any assets")

base_host = urllib.parse.urlparse(base_url).netloc
for asset in sorted(parser.assets):
    url = urllib.parse.urljoin(base_url + "/", asset)
    if urllib.parse.urlparse(url).netloc != base_host:
        continue
    with urllib.request.urlopen(url, timeout=10) as response:
        if response.status != 200 or not response.read(1):
            raise SystemExit(f"dashboard asset is unavailable: {url}")
PY

echo "controller image passed health, readiness, build-metadata, and dashboard asset checks"
