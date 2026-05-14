#!/bin/sh
set -eu

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_cmd docker
require_cmd kubectl

context=$(kubectl config current-context 2>/dev/null || true)
if [ "$context" != "docker-desktop" ]; then
  echo "Expected kubectl context docker-desktop, got: ${context:-<none>}" >&2
  echo "Run: kubectl config use-context docker-desktop" >&2
  exit 1
fi

status_json=$(docker desktop kubernetes status --format json 2>/dev/null || true)
mode=$(printf '%s' "$status_json" | sed -n 's/.*"mode":[[:space:]]*"\([^"]*\)".*/\1/p')
node_count=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')

if [ -z "$mode" ]; then
  echo "Could not determine Docker Desktop Kubernetes mode from the Docker CLI." >&2
  echo "Open Docker Desktop > Kubernetes and confirm the cluster is running." >&2
  exit 1
fi

if [ "$mode" = "kind" ]; then
  echo "Docker Desktop Kubernetes is running in kind mode with ${node_count} node(s)." >&2
  echo "This repo's local-image workflow does not work against Docker Desktop kind clusters using the regular Docker image store." >&2
  echo "Simplest fix for this repo: recreate the Docker Desktop cluster in kubeadm mode, then rerun make k8s-local-apply." >&2
  echo "Alternative: keep kind mode and use a registry-backed image workflow instead of local-only images." >&2
  exit 1
fi

if [ "$mode" != "kubeadm" ]; then
  echo "Unsupported Docker Desktop Kubernetes mode: $mode" >&2
  exit 1
fi

if ! docker image inspect stormrunner-controller:0.1.0 >/dev/null 2>&1; then
  echo "Missing local image stormrunner-controller:0.1.0" >&2
  echo "Run: make docker-controller" >&2
  exit 1
fi

if ! docker image inspect stormrunner-generator:0.1.0 >/dev/null 2>&1; then
  echo "Missing local image stormrunner-generator:0.1.0" >&2
  echo "Run: make docker-generator" >&2
  exit 1
fi

echo "Preflight passed: Docker Desktop Kubernetes is in kubeadm mode and required local images exist."
