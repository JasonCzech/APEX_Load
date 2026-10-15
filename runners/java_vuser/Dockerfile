FROM golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/playwright-go/ ./third_party/playwright-go/
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=
ARG BUILD_TIME=
ARG BI=github.com/jasonczech/apexload/pkg/buildinfo
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X ${BI}.Version=${VERSION} -X ${BI}.Commit=${COMMIT} -X ${BI}.BuildTime=${BUILD_TIME}" \
    -o /apexload-java-vuser-runner ./cmd/apexload-java-vuser-runner

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS harness-builder

RUN apk add --no-cache openjdk21-jdk
WORKDIR /src
COPY runners/java_vuser/Harness.java ./Harness.java
COPY runners/java_vuser/apexload/ ./apexload/
COPY runners/java_vuser/lrapi/lr.java ./lrapi/lr.java
COPY runners/java_vuser/lrapi/SlotBridge.java ./lrapi/SlotBridge.java
COPY runners/java_vuser/fixtures/SmokeActions.java ./fixtures/SmokeActions.java
RUN mkdir -p /classes \
    && javac --release 17 -d /classes lrapi/lr.java Harness.java apexload/data/*.java apexload/bridge/Native*.java \
    && jar --create --file /java-vuser-runner.jar --main-class Harness -C /classes . \
    && mkdir -p /smoke \
    && javac --release 17 -d /smoke fixtures/SmokeActions.java
# Shared JVMs (APEXLOAD_JAVA_VUSER_SHARED_JVM) load the slot host and the
# per-VUser lr shim from these jars; the runner finds them beside the harness.
# Keep in step with the Makefile's java-vuser-slot-runtime target.
RUN mkdir -p /slot-api /slot-shim \
    && javac --release 8 -d /slot-api apexload/bridge/SlotRuntime.java apexload/host/SlotLoader.java apexload/host/SlotWorker.java apexload/host/PooledHost.java apexload/host/PooledWire.java apexload/host/PooledMain.java apexload/host/GCPauses.java apexload/data/*.java apexload/bridge/Native*.java \
    && jar --create --file /apexload-slot-api.jar -C /slot-api . \
    && javac --release 8 -cp /slot-api -d /slot-shim lrapi/lr.java lrapi/SlotBridge.java \
    && jar --create --file /apexload-slot-shim.jar -C /slot-shim .

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

RUN apk add --no-cache ca-certificates curl openjdk21-jre-headless \
    && mkdir -p /opt/apexload/java-vuser /opt/java /tmp \
    && ln -s /usr/lib/jvm/java-21-openjdk /opt/java/openjdk \
    && chown -R 65532:65532 /opt/apexload /tmp

COPY --from=go-builder /apexload-java-vuser-runner /usr/local/bin/apexload-java-vuser-runner
COPY --from=harness-builder /java-vuser-runner.jar /opt/apexload/java-vuser/java-vuser-runner.jar
COPY --from=harness-builder /smoke/SmokeActions.class /opt/apexload/java-vuser/smoke/SmokeActions.class
COPY --from=harness-builder /apexload-slot-api.jar /opt/apexload/java-vuser/apexload-slot-api.jar
COPY --from=harness-builder /apexload-slot-shim.jar /opt/apexload/java-vuser/apexload-slot-shim.jar

ENV JAVA_HOME=/opt/java/openjdk \
    JRE_HOME=/opt/java/openjdk \
    PATH=/opt/java/openjdk/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    APEXLOAD_JAVA_VUSER_RUNNER_ADDR=127.0.0.1:8098 \
    APEXLOAD_JAVA_VUSER_HARNESS_JAR=/opt/apexload/java-vuser/java-vuser-runner.jar \
    APEXLOAD_JAVA_VUSER_DEFAULT_JDK_HOME=/opt/java/openjdk \
    APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp

EXPOSE 8098
# This stock non-privileged image intentionally has no writable cgroup
# delegation. It can serve legacy full_lifecycle requests; persistent phase
# lifecycle capability is advertised only when an operator runs it in a
# separately governed environment with strong process containment.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/apexload-java-vuser-runner"]
