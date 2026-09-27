#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF_DIR="${APEXLOAD_TERRAFORM_DIR:-$ROOT_DIR/deployments/aws/terraform}"
OVERLAY_DIR="${APEXLOAD_EKS_OVERLAY_DIR:-$ROOT_DIR/deployments/k8s/eks}"
OUTPUT_PATH="${APEXLOAD_EKS_RENDER_OUTPUT:-$ROOT_DIR/artifacts/eks/apexload-eks.yaml}"
IMAGE_TAG="${APEXLOAD_IMAGE_TAG:-}"

usage() {
  cat <<'USAGE'
Usage: scripts/render-eks-manifests.sh [--terraform-dir DIR] [--overlay-dir DIR] [--output PATH] [--image-tag TAG]

Renders the EKS Kustomize overlay with Terraform outputs substituted. The
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

AWS_REGION_VALUE="${APEXLOAD_AWS_REGION:-$(tf_output aws_region)}"
ECR_CONTROLLER_REPO="${APEXLOAD_ECR_CONTROLLER_REPOSITORY_URL:-$(tf_output ecr_controller_repository_url)}"
ECR_GENERATOR_REPO="${APEXLOAD_ECR_GENERATOR_REPOSITORY_URL:-$(tf_output ecr_generator_repository_url)}"
ARCHIVE_BUCKET_NAME="${APEXLOAD_ARCHIVE_BUCKET_NAME:-$(tf_output archive_bucket_name)}"
WRAPPING_KMS_KEY_ARN="${APEXLOAD_WRAPPING_KMS_KEY_ARN:-$(tf_output wrapping_kms_key_arn)}"
CONTROLLER_IRSA_ROLE_ARN="${APEXLOAD_CONTROLLER_IRSA_ROLE_ARN:-$(tf_output controller_irsa_role_arn)}"
GENERATOR_IRSA_ROLE_ARN="${APEXLOAD_GENERATOR_IRSA_ROLE_ARN:-$(tf_output generator_irsa_role_arn)}"

# A single-identity deployment may share one role between both workloads. The
# reverse default is deliberately NOT offered: the generator role is the
# read-only one, so silently promoting it to the controller's authority would
# hand secret-rotation rights to every generator pod.
if [[ -z "$GENERATOR_IRSA_ROLE_ARN" ]]; then
  GENERATOR_IRSA_ROLE_ARN="$CONTROLLER_IRSA_ROLE_ARN"
fi
if [[ -z "$IMAGE_TAG" ]]; then
  IMAGE_TAG="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
fi
if [[ -z "$IMAGE_TAG" ]]; then
  IMAGE_TAG="0.1.0"
fi

FLEET_ID_VALUE="${APEXLOAD_FLEET_ID:-}"
if [[ ! "$FLEET_ID_VALUE" =~ ^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$ ]]; then
  echo "set APEXLOAD_FLEET_ID to a unique 1-128 character installation ID" >&2
  exit 1
fi
export FLEET_ID_VALUE

missing=()
[[ -n "$AWS_REGION_VALUE" ]] || missing+=("aws_region")
[[ -n "$ECR_CONTROLLER_REPO" ]] || missing+=("ecr_controller_repository_url")
[[ -n "$ECR_GENERATOR_REPO" ]] || missing+=("ecr_generator_repository_url")
[[ -n "$ARCHIVE_BUCKET_NAME" ]] || missing+=("archive_bucket_name")
[[ -n "$WRAPPING_KMS_KEY_ARN" ]] || missing+=("wrapping_kms_key_arn")
[[ -n "$CONTROLLER_IRSA_ROLE_ARN" ]] || missing+=("controller_irsa_role_arn")
[[ -n "$GENERATOR_IRSA_ROLE_ARN" ]] || missing+=("generator_irsa_role_arn")
if [[ ${#missing[@]} -gt 0 ]]; then
  printf 'missing Terraform outputs: %s\n' "${missing[*]}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/apexload-eks.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
# Render at the source location so relative resources in HA/monitoring overlays
# resolve correctly; substitute the complete output, including inherited bases.
kubectl kustomize "$OVERLAY_DIR" > "$TMP_DIR/rendered.yaml"

export AWS_REGION_VALUE ECR_CONTROLLER_REPO ECR_GENERATOR_REPO ARCHIVE_BUCKET_NAME \
  WRAPPING_KMS_KEY_ARN CONTROLLER_IRSA_ROLE_ARN GENERATOR_IRSA_ROLE_ARN IMAGE_TAG

# Substitute across every YAML in the overlay so additional overlays (eks-ha,
# eks-prometheus) render too, not just the base files. The substitutions are
# placeholder-specific, so applying them to files without placeholders is a
# no-op.
#
# The KMS key ARN is replaced BEFORE the bare region token, because the ARN
# itself embeds "changeme-region" and would otherwise be rewritten twice.
perl -0pi \
  -e 's/\$\{APEXLOAD_FLEET_ID\}/$ENV{FLEET_ID_VALUE}/g;' \
  -e 's#arn:aws:kms:changeme-region:000000000000:key/change-me#$ENV{WRAPPING_KMS_KEY_ARN}#g;' \
  -e 's/changeme\.dkr\.ecr\.changeme-region\.amazonaws\.com\/apexload-controller/$ENV{ECR_CONTROLLER_REPO}/g;' \
  -e 's/changeme\.dkr\.ecr\.changeme-region\.amazonaws\.com\/apexload-generator/$ENV{ECR_GENERATOR_REPO}/g;' \
  -e 's/changeme-apexload-archives/$ENV{ARCHIVE_BUCKET_NAME}/g;' \
  -e 's/changeme-region/$ENV{AWS_REGION_VALUE}/g;' \
  -e 's/\$\{CONTROLLER_IRSA_ROLE_ARN\}/$ENV{CONTROLLER_IRSA_ROLE_ARN}/g;' \
  -e 's/\$\{GENERATOR_IRSA_ROLE_ARN\}/$ENV{GENERATOR_IRSA_ROLE_ARN}/g;' \
  "$TMP_DIR/rendered.yaml"

perl -0pi -e 's#(image:\s*\S*apexload-[a-z0-9-]+):0\.1\.0#$1:$ENV{IMAGE_TAG}#g;' "$TMP_DIR/rendered.yaml"

mkdir -p "$(dirname "$OUTPUT_PATH")"
cp "$TMP_DIR/rendered.yaml" "$OUTPUT_PATH"

if grep -En 'CHANGE-ME|changeme|change-me|\$\{[A-Z0-9_]+\}' "$OUTPUT_PATH" >&2; then
  echo "rendered manifest still contains placeholders" >&2
  exit 1
fi

echo "Rendered EKS manifest: $OUTPUT_PATH"
