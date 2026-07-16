#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'Kubernetes preflight: %s\n' "$*" >&2
  exit 1
}

active_run_override_approval="${APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL:-}"
unset APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL

namespace="${1:-}"
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || die "invalid namespace"
kubectl get namespace "$namespace" >/dev/null || die "namespace $namespace must already exist"
[[ "$(kubectl get namespace "$namespace" -o jsonpath='{.metadata.labels.pod-security\.kubernetes\.io/enforce}')" == restricted ]] \
  || die "namespace $namespace must enforce the restricted Pod Security Standard"
[[ "$(kubectl auth can-i get nodes)" == yes ]] \
  || die "deployment identity cannot read node architecture for digest verification"

for permission in \
  'get deployments.apps' \
  'patch deployments.apps' \
  'get statefulsets.apps' \
  'patch statefulsets.apps' \
  'get secrets'; do
  read -r verb resource <<< "$permission"
  [[ "$(kubectl auth can-i "$verb" "$resource" --namespace "$namespace")" == yes ]] \
    || die "deployment identity cannot $verb $resource in $namespace"
done

required_secrets="${APEXLOAD_REQUIRED_KUBERNETES_SECRETS:-apexload-tls apexload-controller-grpc-tls apexload-generator-grpc-tls apexload-data-platform}"
for secret in $required_secrets; do
  kubectl --namespace "$namespace" get secret "$secret" >/dev/null \
    || die "required secret $secret is missing"
done

ingress_tls_secret="${APEXLOAD_INGRESS_TLS_SECRET:-apexload-tls}"
for key in tls.crt tls.key; do
  value="$(kubectl --namespace "$namespace" get secret "$ingress_tls_secret" -o json 2>/dev/null | jq -r --arg key "$key" '.data[$key] // empty' || true)"
  [[ -n "$value" ]] || die "$ingress_tls_secret is missing $key"
done
grpc_tls_secrets="${APEXLOAD_GRPC_TLS_SECRETS:-apexload-controller-grpc-tls apexload-generator-grpc-tls}"
for secret in $grpc_tls_secrets; do
  for key in tls.crt tls.key ca.crt; do
    value="$(kubectl --namespace "$namespace" get secret "$secret" -o json 2>/dev/null | jq -r --arg key "$key" '.data[$key] // empty' || true)"
    [[ -n "$value" ]] || die "$secret is missing $key"
  done
done

default_storage_classes="$(kubectl get storageclass -o json | jq '[.items[] | select(.metadata.annotations["storageclass.kubernetes.io/is-default-class"] == "true" or .metadata.annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true")] | length')"
(( default_storage_classes > 0 )) || die "no default StorageClass is configured"

APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL="$active_run_override_approval" \
  "$(dirname "${BASH_SOURCE[0]}")/active-runs-preflight.sh" \
  "${APEXLOAD_CONTROLLER_URL:-}"
active_run_override_approval=''

printf 'Kubernetes prerequisites and active-run deployment policy verified\n'
