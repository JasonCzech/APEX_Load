#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF_DIR="${APEXLOAD_TERRAFORM_DIR:-$ROOT_DIR/deployments/azure/terraform}"
OVERLAY_DIR="${APEXLOAD_AKS_OVERLAY_DIR:-$ROOT_DIR/deployments/k8s/aks}"
OUTPUT_PATH="${APEXLOAD_AKS_RENDER_OUTPUT:-$ROOT_DIR/artifacts/aks/apexload-aks.yaml}"
IMAGE_TAG="${APEXLOAD_IMAGE_TAG:-}"

usage() {
  cat <<'USAGE'
Usage: scripts/render-aks-manifests.sh [--terraform-dir DIR] [--overlay-dir DIR] [--output PATH] [--image-tag TAG]

Renders the AKS Kustomize overlay with Terraform outputs substituted. The
script fails if any CHANGE-ME, changeme, change-me, or ${PLACEHOLDER} values
remain in the rendered manifest.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --terraform-dir)
      TF_DIR="$2"
      shift 2
      ;;
    --overlay-dir)
      OVERLAY_DIR="$2"
      shift 2
      ;;
    --output)
      OUTPUT_PATH="$2"
      shift 2
      ;;
    --image-tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "$1 is required" >&2
    exit 1
  fi
}

tf_output() {
  local name="$1"
  terraform -chdir="$TF_DIR" output -raw "$name" 2>/dev/null || true
}

require_tool terraform
require_tool kubectl

ACR_LOGIN_SERVER="${APEXLOAD_ACR_LOGIN_SERVER:-$(tf_output acr_login_server)}"
STORAGE_ACCOUNT_NAME="${APEXLOAD_STORAGE_ACCOUNT_NAME:-$(tf_output storage_account_name)}"
KEY_VAULT_URL="${APEXLOAD_KEY_VAULT_URL:-$(tf_output key_vault_url)}"
CONTROLLER_CLIENT_ID="${APEXLOAD_CONTROLLER_WORKLOAD_IDENTITY_CLIENT_ID:-$(tf_output controller_workload_identity_client_id)}"
GENERATOR_CLIENT_ID="${APEXLOAD_GENERATOR_WORKLOAD_IDENTITY_CLIENT_ID:-$(tf_output generator_workload_identity_client_id)}"
KEDA_CLIENT_ID="${APEXLOAD_KEDA_WORKLOAD_IDENTITY_CLIENT_ID:-$(tf_output keda_workload_identity_client_id)}"

if [[ -z "$CONTROLLER_CLIENT_ID" ]]; then
  CONTROLLER_CLIENT_ID="$(tf_output workload_identity_client_id)"
fi
if [[ -z "$GENERATOR_CLIENT_ID" ]]; then
  GENERATOR_CLIENT_ID="$CONTROLLER_CLIENT_ID"
fi
# The KEDA TriggerAuthentication (aks-azure-monitor overlay) authenticates the
# scaler with the generator's workload identity; fall back to it when a dedicated
# KEDA identity output is absent.
if [[ -z "$KEDA_CLIENT_ID" ]]; then
  KEDA_CLIENT_ID="$GENERATOR_CLIENT_ID"
fi
if [[ -z "$IMAGE_TAG" ]]; then
  IMAGE_TAG="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
fi
if [[ -z "$IMAGE_TAG" ]]; then
  IMAGE_TAG="0.1.0"
fi

missing=()
[[ -n "$ACR_LOGIN_SERVER" ]] || missing+=("acr_login_server")
[[ -n "$STORAGE_ACCOUNT_NAME" ]] || missing+=("storage_account_name")
[[ -n "$KEY_VAULT_URL" ]] || missing+=("key_vault_url")
[[ -n "$CONTROLLER_CLIENT_ID" ]] || missing+=("controller_workload_identity_client_id")
[[ -n "$GENERATOR_CLIENT_ID" ]] || missing+=("generator_workload_identity_client_id")
if [[ ${#missing[@]} -gt 0 ]]; then
  printf 'missing Terraform outputs: %s\n' "${missing[*]}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/apexload-aks.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
cp -R "$OVERLAY_DIR/." "$TMP_DIR/"

export STORAGE_ACCOUNT_NAME KEY_VAULT_URL ACR_LOGIN_SERVER CONTROLLER_CLIENT_ID GENERATOR_CLIENT_ID KEDA_CLIENT_ID IMAGE_TAG
# Substitute across every YAML in the overlay so additional overlays (e.g.
# aks-azure-monitor's triggerauthentication.yaml carrying
# ${KEDA_WORKLOAD_IDENTITY_CLIENT_ID}) are rendered, not just the base files. The
# substitutions are placeholder-specific, so applying them to files without
# placeholders is a no-op.
perl -0pi \
  -e 's/changemestorageaccount/$ENV{STORAGE_ACCOUNT_NAME}/g;' \
  -e 's#https://change-me\.vault\.azure\.net#$ENV{KEY_VAULT_URL}#g;' \
  -e 's/changeme\.azurecr\.io/$ENV{ACR_LOGIN_SERVER}/g;' \
  -e 's/\$\{CONTROLLER_WORKLOAD_IDENTITY_CLIENT_ID\}/$ENV{CONTROLLER_CLIENT_ID}/g;' \
  -e 's/\$\{GENERATOR_WORKLOAD_IDENTITY_CLIENT_ID\}/$ENV{GENERATOR_CLIENT_ID}/g;' \
  -e 's/\$\{KEDA_WORKLOAD_IDENTITY_CLIENT_ID\}/$ENV{KEDA_CLIENT_ID}/g;' \
  -e 's/\$\{WORKLOAD_IDENTITY_CLIENT_ID\}/$ENV{CONTROLLER_CLIENT_ID}/g;' \
  "$TMP_DIR"/*.yaml

perl -0pi -e 's/newTag: 0\.1\.0/newTag: $ENV{IMAGE_TAG}/g;' "$TMP_DIR"/kustomization.yaml

mkdir -p "$(dirname "$OUTPUT_PATH")"
kubectl kustomize "$TMP_DIR" > "$OUTPUT_PATH"

if grep -En 'CHANGE-ME|changeme|change-me|\$\{[A-Z0-9_]+\}' "$OUTPUT_PATH" >&2; then
  echo "rendered manifest still contains placeholders" >&2
  exit 1
fi

echo "Rendered AKS manifest: $OUTPUT_PATH"
