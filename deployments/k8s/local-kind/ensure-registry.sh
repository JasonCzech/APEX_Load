#!/bin/sh
set -eu

reg_name="apexload-kind-registry"
reg_port="5001"
kind_network="kind"

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
if [ "$mode" != "kind" ]; then
  echo "Expected Docker Desktop Kubernetes kind mode, got: ${mode:-<unknown>}" >&2
  echo "Use deployments/k8s/local for kubeadm mode instead." >&2
  exit 1
fi

if [ "$(docker inspect -f '{{.State.Running}}' "$reg_name" 2>/dev/null || true)" != "true" ]; then
  docker run -d --restart=always -p "127.0.0.1:${reg_port}:5000" --network bridge --name "$reg_name" registry:2 >/dev/null
fi

if [ "$(docker inspect -f '{{json .NetworkSettings.Networks.kind}}' "$reg_name" 2>/dev/null || true)" = "null" ]; then
  docker network connect "$kind_network" "$reg_name"
fi

REGISTRY_DIR="/etc/containerd/certs.d/localhost:${reg_port}"
for node in $(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  docker exec "$node" mkdir -p "$REGISTRY_DIR"
  cat <<EOF | docker exec -i "$node" sh -c "cat > ${REGISTRY_DIR}/hosts.toml"
server = "http://localhost:${reg_port}"

[host."http://${reg_name}:5000"]
EOF
done

cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${reg_port}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

echo "Kind registry ready at localhost:${reg_port} and wired into Docker Desktop Kubernetes nodes."
