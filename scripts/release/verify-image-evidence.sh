#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'image evidence: %s\n' "$*" >&2
  exit 1
}

manifest="${1:-}"
component="${2:-}"
profile="${3:-}"
public_key="${4:-}"

[[ -f "$manifest" && -f "$public_key" ]] || die "manifest and Cosign public key are required"
[[ "$component" == controller || "$component" == generator ]] || die "invalid component"
[[ "$profile" == standard || "$profile" == enterprise ]] || die "invalid profile"
command -v jq >/dev/null || die "jq is required"
command -v cosign >/dev/null || die "cosign is required"
[[ "${APEXLOAD_IMAGE_INSPECTOR:-}" == skopeo ]] \
  || die "APEXLOAD_IMAGE_INSPECTOR must explicitly select skopeo"
command -v skopeo >/dev/null || die "skopeo is required"

row="$(jq -cer --arg component "$component" --arg profile "$profile" '
  [.images[] | select(.component == $component and .profile == $profile)]
  | if length == 1 then .[0] else error("expected exactly one matching image") end
' "$manifest")" || die "release manifest image record is missing or duplicated"
repository="$(jq -er '.repository' <<< "$row")"
digest="$(jq -er '.digest' <<< "$row")"
[[ "$repository" =~ ^[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._/-]+$ ]] || die "invalid image repository"
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid image index digest"

platform_map="$(jq -cer '
  .platforms
  | if length == 2 and
      ([.[].os] | all(. == "linux")) and
      ([.[].architecture] | sort == ["amd64", "arm64"]) and
      (all(.[].digest; test("^sha256:[0-9a-f]{64}$"))) and
      ([.[].digest] | unique | length == 2)
    then map({key: .architecture, value: .digest}) | from_entries
    else error("expected exactly linux/amd64 and linux/arm64 platform evidence")
    end
' <<< "$row")" || die "image platform evidence is incomplete or duplicated"

evidence_tmp="$(mktemp -d)"
trap 'rm -rf "$evidence_tmp"' EXIT
inspect_raw() {
  local reference="$1"
  local output="$2"
  skopeo inspect --raw "docker://${reference}" > "$output" \
    || die "could not inspect immutable image reference ${reference}"
}

inspect_config() {
  local reference="$1"
  local output="$2"
  skopeo inspect --config "docker://${reference}" > "$output" \
    || die "could not inspect immutable image configuration ${reference}"
}

index_file="$evidence_tmp/index.json"
inspect_raw "${repository}@${digest}" "$index_file"
jq -e --argjson expected "$platform_map" '
  def direct_image_manifest:
    .mediaType == "application/vnd.oci.image.manifest.v1+json" or
    .mediaType == "application/vnd.docker.distribution.manifest.v2+json";
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
  all(.manifests[];
    if (.platform.os // "") == "linux" then
      (
        ((.platform.architecture // "") == "amd64" or
         (.platform.architecture // "") == "arm64") and
        direct_image_manifest and
        (.size | type == "number" and floor == . and . > 0) and
        .digest == $expected[.platform.architecture]
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
' "$index_file" >/dev/null \
  || die "immutable image index must contain direct platform manifests matching the signed release manifest"

cosign verify --key "$public_key" "${repository}@${digest}" >/dev/null
platform_count=0
while IFS= read -r platform; do
  os="$(jq -er '.os' <<< "$platform")"
  architecture="$(jq -er '.architecture' <<< "$platform")"
  platform_digest="$(jq -er '.digest' <<< "$platform")"
  sbom_asset="$(jq -er '.sbom_asset' <<< "$platform")"
  sbom_sha256="$(jq -er '.sbom_sha256' <<< "$platform")"
  [[ "$os" == linux ]] || die "unsupported image platform OS"
  [[ "$architecture" == amd64 || "$architecture" == arm64 ]] || die "unsupported image architecture"
  [[ "$platform_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid image platform digest"
  [[ "$sbom_asset" == "sbom-apexload-${component}-${profile}-linux-${architecture}.spdx.json" ]] \
    || die "invalid SBOM asset name"
  [[ "$sbom_sha256" =~ ^[0-9a-f]{64}$ ]] || die "invalid SBOM checksum"
  child_manifest="$evidence_tmp/${architecture}-manifest.json"
  inspect_raw "${repository}@${platform_digest}" "$child_manifest"
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
  ' "$child_manifest" >/dev/null \
    || die "platform digest ${platform_digest} is not a direct runnable image manifest"
  expected_child_size="$(jq -er --arg architecture "$architecture" '
    [.manifests[]
      | select(
        (.platform.os // "") == "linux" and
        (.platform.architecture // "") == $architecture
      )
      | .size
    ]
    | if length == 1 then .[0] else error("missing unique platform descriptor size") end
  ' "$index_file")"
  actual_child_size="$(wc -c < "$child_manifest" | tr -d '[:space:]')"
  [[ "$actual_child_size" == "$expected_child_size" ]] \
    || die "platform digest ${platform_digest} descriptor size does not match its manifest"
  child_config="$evidence_tmp/${architecture}-config.json"
  inspect_config "${repository}@${platform_digest}" "$child_config"
  jq -e --arg architecture "$architecture" '
    .os == "linux" and
    .architecture == $architecture
  ' "$child_config" >/dev/null \
    || die "platform digest ${platform_digest} configuration does not match linux/${architecture}"
  sbom_path="$(dirname "$manifest")/$sbom_asset"
  [[ -s "$sbom_path" ]] || die "signed SBOM asset is missing"
  actual_sbom_sha256="$(sha256sum "$sbom_path" | awk '{print $1}')"
  [[ "$actual_sbom_sha256" == "$sbom_sha256" ]] || die "signed SBOM asset checksum does not match the manifest"
  cosign verify --key "$public_key" "${repository}@${platform_digest}" >/dev/null
  "$(dirname "${BASH_SOURCE[0]}")/verify-sbom-attestation.sh" \
    "${repository}@${platform_digest}" "$public_key" "$sbom_path"
  platform_count=$((platform_count + 1))
done < <(jq -cer '.platforms[]' <<< "$row")
(( platform_count == 2 )) || die "image platform evidence is incomplete"

printf '%s@%s\n' "$repository" "$digest"
