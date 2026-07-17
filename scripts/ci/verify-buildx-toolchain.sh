#!/usr/bin/env bash
set -euo pipefail

readonly EXPECTED_BUILDX_VERSION="v0.34.1"
readonly EXPECTED_BUILDKIT_VERSION="v0.31.1"
readonly EXPECTED_BUILDKIT_DIGEST="sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a"

die() {
  printf 'buildx toolchain verification failed: %s\n' "$*" >&2
  exit 1
}

verify_image_reference() {
  local image="${1:-}"
  [[ "$image" =~ ^[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] \
    || die "BuildKit image must be an immutable registry digest reference"
  [[ "${image##*@}" == "$EXPECTED_BUILDKIT_DIGEST" ]] \
    || die "BuildKit image digest is not the reviewed $EXPECTED_BUILDKIT_VERSION digest"
}

verify_buildx_binary() {
  command -v docker >/dev/null || die "docker is required"
  local version_output
  version_output="$(docker buildx version 2>&1)" || die "docker buildx is unavailable"
  grep -Eq "^github\.com/docker/buildx ${EXPECTED_BUILDX_VERSION}([[:space:]]|$)" <<< "$version_output" \
    || die "expected Docker Buildx $EXPECTED_BUILDX_VERSION"
}

verify_builder() {
  local builder_name="${1:-}"
  local buildkit_image="${2:-}"
  [[ "$builder_name" =~ ^[A-Za-z0-9_.-]+$ ]] || die "invalid builder name"
  verify_image_reference "$buildkit_image"
  verify_buildx_binary

  local inspect_output
  inspect_output="$(docker buildx inspect "$builder_name" --bootstrap 2>&1)" \
    || die "could not inspect builder $builder_name"
  local actual_builder_name actual_driver actual_buildkit_version
  actual_builder_name="$(awk '$1 == "Name:" {print $2; exit}' <<< "$inspect_output")"
  actual_driver="$(awk '$1 == "Driver:" {print $2; exit}' <<< "$inspect_output")"
  actual_buildkit_version="$(awk '$1 == "BuildKit" && $2 == "version:" {print $3; exit}' <<< "$inspect_output")"
  [[ "$actual_builder_name" == "$builder_name" ]] \
    || die "Buildx selected an unexpected builder"
  [[ "$actual_driver" == docker-container ]] \
    || die "release builds require the docker-container driver"
  [[ "$actual_buildkit_version" == "$EXPECTED_BUILDKIT_VERSION" ]] \
    || die "builder is not running BuildKit $EXPECTED_BUILDKIT_VERSION"

  local container_name="buildx_buildkit_${builder_name}0"
  local configured_image container_image_id reviewed_image_id
  configured_image="$(docker inspect --format '{{.Config.Image}}' "$container_name" 2>/dev/null)" \
    || die "could not inspect the BuildKit container"
  [[ "${configured_image##*@}" == "$EXPECTED_BUILDKIT_DIGEST" ]] \
    || die "running BuildKit container was not created from the reviewed image digest"

  container_image_id="$(docker inspect --format '{{.Image}}' "$container_name" 2>/dev/null)" \
    || die "could not resolve the running BuildKit image ID"
  reviewed_image_id="$(docker image inspect --format '{{.Id}}' "$configured_image" 2>/dev/null)" \
    || die "could not resolve the reviewed BuildKit image"
  [[ "$container_image_id" == "$reviewed_image_id" ]] \
    || die "running BuildKit container image does not match its immutable image reference"
}

case "${1:-}" in
  image)
    [[ "$#" -eq 2 ]] || die "usage: $0 image <buildkit-image>"
    verify_image_reference "$2"
    ;;
  binary)
    [[ "$#" -eq 1 ]] || die "usage: $0 binary"
    verify_buildx_binary
    ;;
  builder)
    [[ "$#" -eq 3 ]] || die "usage: $0 builder <builder-name> <buildkit-image>"
    verify_builder "$2" "$3"
    ;;
  *)
    die "usage: $0 <image|binary|builder> ..."
    ;;
esac
