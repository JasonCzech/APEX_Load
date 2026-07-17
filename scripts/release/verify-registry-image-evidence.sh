#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'registry image evidence: %s\n' "$*" >&2
  exit 1
}

image="${1:-}"
public_key="${2:-}"
amd64_sbom="${3:-}"
arm64_sbom="${4:-}"
[[ "$image" =~ ^[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] \
  || die "an immutable image reference is required"
[[ -f "$public_key" ]] || die "Cosign public key is required"
[[ -s "$amd64_sbom" && -s "$arm64_sbom" ]] || die "exact amd64 and arm64 SBOM assets are required"
command -v cosign >/dev/null || die "cosign is required"
command -v skopeo >/dev/null || die "skopeo is required"
command -v jq >/dev/null || die "jq is required"

evidence_tmp="$(mktemp -d)"
trap 'rm -rf "$evidence_tmp"' EXIT

cosign verify --key "$public_key" "$image" >/dev/null
index="$(skopeo inspect --raw "docker://${image}")" || die "could not inspect image index"
platform_count=0
while IFS=$'\t' read -r architecture digest expected_child_size; do
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid platform digest"
  [[ "$architecture" == amd64 || "$architecture" == arm64 ]] || die "invalid platform architecture"
  [[ "$expected_child_size" =~ ^[1-9][0-9]*$ ]] || die "invalid platform descriptor size"
  repository="${image%@*}"
  child_manifest_file="$evidence_tmp/${architecture}-manifest.json"
  skopeo inspect --raw "docker://${repository}@${digest}" > "$child_manifest_file" \
    || die "could not inspect platform image manifest"
  jq -e '
    def runnable_layer:
      .mediaType == "application/vnd.oci.image.layer.v1.tar" or
      .mediaType == "application/vnd.oci.image.layer.v1.tar+gzip" or
      .mediaType == "application/vnd.oci.image.layer.v1.tar+zstd" or
      .mediaType == "application/vnd.oci.image.layer.nondistributable.v1.tar" or
      .mediaType == "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip" or
      .mediaType == "application/vnd.oci.image.layer.nondistributable.v1.tar+zstd" or
      .mediaType == "application/vnd.docker.image.rootfs.diff.tar.gzip" or
      .mediaType == "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip";
    .schemaVersion == 2 and
    (
      .mediaType == "application/vnd.oci.image.manifest.v1+json" or
      .mediaType == "application/vnd.docker.distribution.manifest.v2+json"
    ) and
    (has("manifests") | not) and
    (.config | type == "object") and
    (
      .config.mediaType == "application/vnd.oci.image.config.v1+json" or
      .config.mediaType == "application/vnd.docker.container.image.v1+json"
    ) and
    (.config.digest | test("^sha256:[0-9a-f]{64}$")) and
    (.config.size | type == "number" and floor == . and . > 0) and
    (.layers | type == "array") and
    all(.layers[];
      runnable_layer and
      (.digest | test("^sha256:[0-9a-f]{64}$")) and
      (.size | type == "number" and floor == . and . > 0)
    )
  ' "$child_manifest_file" >/dev/null \
    || die "platform digest ${digest} is not a direct runnable image manifest"
  actual_child_size="$(wc -c < "$child_manifest_file" | tr -d '[:space:]')"
  [[ "$actual_child_size" == "$expected_child_size" ]] \
    || die "platform digest ${digest} descriptor size does not match its manifest"
  child_config="$(skopeo inspect --config "docker://${repository}@${digest}")" \
    || die "could not inspect platform image configuration"
  jq -e --arg architecture "$architecture" '
    .os == "linux" and
    .architecture == $architecture
  ' <<< "$child_config" >/dev/null \
    || die "platform digest ${digest} configuration does not match linux/${architecture}"
  sbom="$amd64_sbom"
  [[ "$architecture" == arm64 ]] && sbom="$arm64_sbom"
  cosign verify --key "$public_key" "${repository}@${digest}" >/dev/null
  "$(dirname "${BASH_SOURCE[0]}")/verify-sbom-attestation.sh" \
    "${repository}@${digest}" "$public_key" "$sbom"
  platform_count=$((platform_count + 1))
done < <(jq -er '
  def direct_image_manifest:
    .mediaType == "application/vnd.oci.image.manifest.v1+json" or
    .mediaType == "application/vnd.docker.distribution.manifest.v2+json";
  . as $index
  | (
    .schemaVersion == 2 and
    (.manifests | type == "array") and
    ([.manifests[] | select(
      (.platform.os // "") == "linux" and
      ((.platform.architecture // "") == "amd64" or
       (.platform.architecture // "") == "arm64")
    )] | length == 2) and
    ([.manifests[]
      | select(
        (.platform.os // "") == "linux" and
        ((.platform.architecture // "") == "amd64" or
         (.platform.architecture // "") == "arm64")
      )
      | .platform.architecture
    ] | sort == ["amd64", "arm64"]) and
    ([.manifests[]
      | select(
        (.platform.os // "") == "linux" and
        ((.platform.architecture // "") == "amd64" or
         (.platform.architecture // "") == "arm64")
      )
      | .digest
    ] | unique | length == 2) and
    all(.manifests[];
      if (.platform.os // "") == "linux" then
        (
          ((.platform.architecture // "") == "amd64" or
           (.platform.architecture // "") == "arm64") and
          direct_image_manifest and
          (.size | type == "number" and floor == . and . > 0) and
          (.digest | test("^sha256:[0-9a-f]{64}$"))
        )
      else
        (
          (.platform.os // "") == "unknown" and
          (.platform.architecture // "") == "unknown" and
          .mediaType == "application/vnd.oci.image.manifest.v1+json" and
          (.annotations["vnd.docker.reference.type"] // "") == "attestation-manifest"
        )
      end
    )
  )
  | if . then
      $index.manifests[]
      | select(
        (.platform.os // "") == "linux" and
        ((.platform.architecture // "") == "amd64" or
         (.platform.architecture // "") == "arm64")
      )
      | [.platform.architecture, .digest, (.size | tostring)]
      | @tsv
    else
      error("expected exactly direct linux/amd64 and linux/arm64 image manifests")
    end
' <<< "$index")
(( platform_count == 2 )) || die "image platform evidence is incomplete"

printf 'verified signatures and SPDX attestations for %s\n' "$image"
