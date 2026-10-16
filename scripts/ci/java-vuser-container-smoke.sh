#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "${1// }" ]]; then
  echo "usage: $0 <java-vuser-runner-image>" >&2
  exit 2
fi

image=$1
name="apexload-java-vuser-smoke-${RANDOM}-$$"
auth_token="java-vuser-ci-smoke-${RANDOM}-$$"
owner_id="ci-container-smoke-owner"
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
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777 \
  --env APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true \
  --env "APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN=${auth_token}" \
  "${image}" >/dev/null

# The runner deliberately listens only on the container's loopback interface.
# Probe it from inside that network namespace; a published Docker port targets
# the container's external interface and must not make the endpoint reachable.
base_url="http://127.0.0.1:8098"
for _ in $(seq 1 60); do
  if docker exec "${name}" curl --silent --show-error --fail "${base_url}/readyz" \
    >"${workdir}/health.json"; then
    break
  fi
  sleep 1
done

if [[ ! -s "${workdir}/health.json" ]]; then
  echo "Java Vuser runner did not become healthy within 60 seconds" >&2
  exit 1
fi

# Health endpoints remain unauthenticated for orchestration, but every
# capability or mutation endpoint must reject a request without the shared
# generator-to-runner credential.
unauthorized_status=$(docker exec "${name}" curl --silent --output /dev/null \
  --write-out '%{http_code}' "${base_url}/v1/capabilities")
if [[ "${unauthorized_status}" != "401" ]]; then
  echo "Java Vuser runner accepted an unauthenticated protected request" >&2
  exit 1
fi

docker exec "${name}" curl --silent --show-error --fail "${base_url}/v1/capabilities" \
  --header "X-Apexload-Java-Runner-Token: ${auth_token}" \
  >"${workdir}/capabilities.json"
jq -e '
  .contract == "apexload-java-vuser-runner.v1" and
  .healthy == true and
  .minimum_java_major == 17 and
  .java_major >= 17 and
  .max_processes > 0 and
  (.features | index("java_vuser_persistent_lifecycle_v1") == null) and
  (.features | index("java_vuser_phase_lifecycle_v1") == null)
' "${workdir}/capabilities.json" >/dev/null

jq -n --arg owner_id "${owner_id}" '{
  contract: "apexload-java-vuser-runner.v1",
  owner_id: $owner_id
}' >"${workdir}/claim-request.json"
docker exec -i "${name}" curl --silent --show-error --fail \
  --header "X-Apexload-Java-Runner-Token: ${auth_token}" \
  --header 'Content-Type: application/json' \
  --data-binary @- \
  "${base_url}/v1/owner/claim" \
  <"${workdir}/claim-request.json" >"${workdir}/claim-response.json"
owner_epoch=$(jq -er --arg owner_id "${owner_id}" '
  select(
    .contract == "apexload-java-vuser-runner.v1" and
    .owner_id == $owner_id and
    .claimed == true and
    (.epoch | type == "string" and length > 0)
  ) | .epoch
' "${workdir}/claim-response.json")

jq -n '{
  contract: "apexload-java-vuser-runner.v1",
  runtime: {
    classpath: ["/opt/apexload/java-vuser/smoke"],
    additional_vm_parameters: ["-Djava_vuser.smoke=ci"],
    use_xbootclasspath: true,
    dedicated_class_loader: true
  },
  targets: [{class_name: "SmokeActions", method: "full_lifecycle"}],
  timeout_ms: 10000
}' >"${workdir}/validate-request.json"
docker exec -i "${name}" curl --silent --show-error --fail \
  --header "X-Apexload-Java-Runner-Token: ${auth_token}" \
  --header 'Content-Type: application/json' \
  --data-binary @- \
  "${base_url}/v1/validate" \
  <"${workdir}/validate-request.json" >"${workdir}/validate-response.json"
jq -e '
  .contract == "apexload-java-vuser-runner.v1" and
  .valid == true and
  (.warning_codes | index("java_vuser_xbootclasspath_modern_fallback") != null)
' "${workdir}/validate-response.json" >/dev/null

# Every execution runs under a runtime lease the current owner claims first.
jq -n --arg owner_id "${owner_id}" --arg owner_epoch "${owner_epoch}" '{
  contract: "apexload-java-vuser-runner.v1",
  owner_id: $owner_id,
  owner_epoch: $owner_epoch
}' >"${workdir}/lease-request.json"
docker exec -i "${name}" curl --silent --show-error --fail \
  --header "X-Apexload-Java-Runner-Token: ${auth_token}" \
  --header 'Content-Type: application/json' \
  --data-binary @- \
  "${base_url}/v1/runtime-leases/claim" \
  <"${workdir}/lease-request.json" >"${workdir}/lease-response.json"
lease_id=$(jq -er '
  select(
    .contract == "apexload-java-vuser-runner.v1" and
    .claimed == true and
    (.lease_token | type == "string" and length > 0)
  ) | .lease_id | select(type == "string" and length > 0)
' "${workdir}/lease-response.json")
lease_token=$(jq -er '.lease_token' "${workdir}/lease-response.json")

jq -n --arg owner_id "${owner_id}" --arg owner_epoch "${owner_epoch}" \
  --arg lease_id "${lease_id}" --arg lease_token "${lease_token}" '{
  contract: "apexload-java-vuser-runner.v1",
  execution_id: "ci-container-smoke",
  owner_id: $owner_id,
  owner_epoch: $owner_epoch,
  runtime_lease_id: $lease_id,
  runtime_lease_token: $lease_token,
  runtime: {
    classpath: ["/opt/apexload/java-vuser/smoke"],
    additional_vm_parameters: ["-Djava_vuser.smoke=ci"],
    use_xbootclasspath: true,
    dedicated_class_loader: true
  },
  command: {
    action_name: "container-smoke",
    action_type: "java_vuser_run",
    class_name: "SmokeActions",
    method: "full_lifecycle"
  },
  timeout_ms: 10000
}' >"${workdir}/execute-request.json"
docker exec -i "${name}" curl --silent --show-error --fail \
  --header "X-Apexload-Java-Runner-Token: ${auth_token}" \
  --header 'Content-Type: application/json' \
  --data-binary @- \
  "${base_url}/v1/executions" \
  <"${workdir}/execute-request.json" >"${workdir}/execute-response.json"
jq -e '
  .contract == "apexload-java-vuser-runner.v1" and
  .execution_id == "ci-container-smoke" and
  .success == true and
  (.warning_codes | index("java_vuser_xbootclasspath_modern_fallback") != null)
' "${workdir}/execute-response.json" >/dev/null

docker inspect --format '{{json .Config.User}} {{json .HostConfig.ReadonlyRootfs}}' "${name}" \
  | grep -Eq '^"65532:65532" true$'
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${name}" \
  | grep -qx 'APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp'

echo "Java Vuser runner image passed honest legacy-only capability, health, JDK 17+, validation, dedicated-loader execution, writable-workdir, and read-only checks"
