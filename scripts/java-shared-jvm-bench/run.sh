#!/usr/bin/env bash
# Benchmarks dedicated against shared Java VUser JVMs on a Linux runner in
# Docker, with the middleware-heavy LR-style evidence fixture (Apache
# HttpClient, Jackson, Guava) against a built-in order service.
#
#   scripts/java-shared-jvm-bench/run.sh latency        # 25 VUsers, dedicated vs shared
#   PACE_MS=50 scripts/java-shared-jvm-bench/run.sh latency   # the same at a fixed pace
#   scripts/java-shared-jvm-bench/run.sh shared         # 25 VUsers, shared only
#   scripts/java-shared-jvm-bench/run.sh scale 5000     # shared only, paced
#     (DURATION=240s WARMUP=75s measures latency after the 60 s ramp)
#
# The runner container is privileged with a private cgroup namespace so the
# runner gets the delegated cgroup v2 subtree that persistent lifecycles and
# shared JVMs require. That is acceptable for a disposable local benchmark,
# not for a shared cluster. Nothing is pulled: IMAGE must already be local.
set -euo pipefail

repo="$(cd "$(dirname "$0")/../.." && pwd)"
# Docker Desktop on macOS stalls bind mounts from privacy-protected folders
# such as ~/Documents behind a permission prompt; a temporary directory avoids
# that.
out="${OUT_DIR:-${TMPDIR:-/tmp}/apexload-java-shared-jvm-bench}"
image="${IMAGE:-eclipse-temurin:21-jre}"
m2="${M2_REPOSITORY:-$HOME/.m2/repository}"
name="apexload-java-shared-jvm-bench"
memory="${MEMORY:-6500m}"
cpus="${CPUS:-6}"
duration="${DURATION:-60s}"
mode="${1:-latency}"

case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) goarch=arm64 ;;
  x86_64|amd64) goarch=amd64 ;;
  *) echo "unsupported Docker architecture" >&2; exit 1 ;;
esac
docker image inspect "$image" >/dev/null

# Remove only what this script writes, never the rest of OUT_DIR.
rm -rf -- "$out/java-vuser" "$out/fixture" "$out/classes" "$out/runner" "$out/bench"
mkdir -p "$out/java-vuser" "$out/fixture/classes" "$out/fixture/lib" "$out/classes/harness" "$out/classes/api" "$out/classes/shim"
(cd "$repo" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$out/runner" ./cmd/apexload-java-vuser-runner)
(cd "$repo" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$out/bench" ./scripts/java-shared-jvm-bench)

# The same harness, slot API and shim jars the runner image and the Makefile's
# java-vuser-runner and java-vuser-slot-runtime targets build.
src="$repo/runners/java_vuser"
(cd "$src" && javac --release 17 -d "$out/classes/harness" lrapi/lr.java Harness.java apexload/data/*.java apexload/bridge/Native*.java)
jar --create --file "$out/java-vuser/java-vuser-runner.jar" --main-class Harness -C "$out/classes/harness" .
(cd "$src" && javac --release 8 -Xlint:-options -d "$out/classes/api" apexload/bridge/SlotRuntime.java apexload/host/SlotLoader.java apexload/host/SlotWorker.java apexload/host/PooledHost.java apexload/host/PooledWire.java apexload/host/PooledMain.java apexload/host/GCPauses.java apexload/data/*.java apexload/bridge/Native*.java)
jar --create --file "$out/java-vuser/apexload-slot-api.jar" -C "$out/classes/api" .
(cd "$src" && javac --release 8 -Xlint:-options -cp "$out/classes/api" -d "$out/classes/shim" lrapi/lr.java lrapi/SlotBridge.java)
jar --create --file "$out/java-vuser/apexload-slot-shim.jar" -C "$out/classes/shim" .

# The fixture's dependencies come from the local Maven cache (see
# runners/java_vuser/evidence/pom.xml); nothing is downloaded.
deps=(
  org/apache/httpcomponents/httpclient/4.5.14/httpclient-4.5.14.jar
  org/apache/httpcomponents/httpcore/4.4.16/httpcore-4.4.16.jar
  commons-logging/commons-logging/1.2/commons-logging-1.2.jar
  commons-codec/commons-codec/1.11/commons-codec-1.11.jar
  com/fasterxml/jackson/core/jackson-databind/2.16.2/jackson-databind-2.16.2.jar
  com/fasterxml/jackson/core/jackson-core/2.16.2/jackson-core-2.16.2.jar
  com/fasterxml/jackson/core/jackson-annotations/2.16.2/jackson-annotations-2.16.2.jar
  com/google/guava/guava/33.0.0-jre/guava-33.0.0-jre.jar
  com/google/guava/failureaccess/1.0.2/failureaccess-1.0.2.jar
)
compile_cp="$out/java-vuser/java-vuser-runner.jar"
runner_cp="/bench/fixture/heavy.jar"
for dep in "${deps[@]}"; do
  cp "$m2/$dep" "$out/fixture/lib/"
  compile_cp="$compile_cp:$out/fixture/lib/$(basename "$dep")"
  runner_cp="$runner_cp,/bench/fixture/lib/$(basename "$dep")"
done
javac --release 8 -Xlint:-options -cp "$compile_cp" -d "$out/fixture/classes" "$src/evidence/HeavyActions.java"
jar --create --file "$out/fixture/heavy.jar" -C "$out/fixture/classes" .

token="$(openssl rand -hex 24)"
docker rm -f "$name" >/dev/null 2>&1 || true
cleanup() {
  if [ -n "${KEEP_LOGS:-}" ]; then
    docker logs "$name" >"$out/runner.log" 2>&1 || true
  fi
  docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker run -d --pull=never --name "$name" --privileged --cgroupns=private \
  --memory "$memory" --memory-swap "$memory" --cpus "$cpus" --network none \
  --tmpfs /tmp:rw,exec,size=1g \
  --mount "type=bind,src=$out,dst=/bench,readonly" \
  -e APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true \
  -e APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN="$token" \
  -e APEXLOAD_JAVA_VUSER_RUNNER_ADDR=127.0.0.1:8098 \
  -e APEXLOAD_JAVA_VUSER_HARNESS_JAR=/bench/java-vuser/java-vuser-runner.jar \
  -e APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME=/opt/java/openjdk \
  -e APEXLOAD_JAVA_VUSER_MAX_PROCESSES="${MAX_PROCESSES:-25}" \
  -e APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp \
  -e APEXLOAD_JAVA_VUSER_SHARED_JVM=true \
  -e APEXLOAD_JAVA_VUSER_SHARED_JVM_VUSERS_PER_JVM="${VUSERS_PER_JVM:-1024}" \
  -e APEXLOAD_JAVA_VUSER_SHARED_JVM_HEAP_MB="${HEAP_MB:-320}" \
  -e APEXLOAD_JAVA_VUSER_DEDICATED_JVM_MEMORY_MB="${DEDICATED_JVM_MB:-200}" \
  --entrypoint /bench/runner "$image" >/dev/null

bench() {
  docker exec \
    -e APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true \
    -e APEXLOAD_JAVA_VUSER_RUNNER_MODE=sidecar \
    -e APEXLOAD_JAVA_VUSER_RUNNER_URL=http://127.0.0.1:8098 \
    -e APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN="$token" \
    "$name" /bench/bench -target HeavyActions -classpath "$runner_cp" -vm-args=-Xmx128m "$@"
}

case "$mode" in
  latency)
    vusers="${VUSERS:-25}"
    bench -sharing dedicated -vusers "$vusers" -duration "$duration" -warmup "${WARMUP:-10s}" -pace-ms "${PACE_MS:-0}" | tee "$out/dedicated.json"
    bench -sharing shared -vusers "$vusers" -duration "$duration" -warmup "${WARMUP:-10s}" -pace-ms "${PACE_MS:-0}" | tee "$out/shared.json"
    ;;
  shared)
    bench -sharing shared -vusers "${VUSERS:-25}" -duration "$duration" -warmup "${WARMUP:-10s}" | tee "$out/shared.json"
    ;;
  scale)
    bench -sharing shared -vusers "${2:-5000}" -duration "$duration" -ramp-up "${RAMP_UP:-60s}" -warmup "${WARMUP:-0s}" -pace-ms "${PACE_MS:-5000}" | tee "$out/scale.json"
    ;;
  *)
    echo "usage: $0 latency|shared|scale [vusers]" >&2
    exit 2
    ;;
esac
