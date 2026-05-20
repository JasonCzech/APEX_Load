#!/bin/sh
set -eu

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_cmd docker

for image in apexload-controller:0.1.0 apexload-generator:0.1.0; do
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "Missing local image: $image" >&2
    echo "Build it first with make docker-controller and make docker-generator" >&2
    exit 1
  fi
done

for image in controller generator; do
  docker tag "apexload-${image}:0.1.0" "localhost:5001/apexload-${image}:0.1.0"
  docker push "localhost:5001/apexload-${image}:0.1.0"
done

echo "Pushed APEX Load images to localhost:5001 for the Docker Desktop kind cluster."
