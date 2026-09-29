.PHONY: all build test test-jms-solace-real import-policy-locality performance-smoke performance-evidence clean generator controller apexctl recording-agent collector-agent package-recording-agent desktop desktop-release desktop-api-binaries clients desktop-preflight clients-release java-vuser-runner java-vuser-runner-legacy proto generate lint docker-generator docker-controller docker-local-up docker-local-down docker-local-logs docker-local-data-up docker-local-data-down docker-local-data-logs docker-local-prometheus-check docker-push docker-push-acr k8s-local-preflight k8s-local-apply k8s-local-status k8s-local-delete k8s-kind-registry-setup k8s-kind-push-images k8s-kind-apply k8s-kind-status k8s-kind-delete k8s-aks-render k8s-aks-apply k8s-aks-status k8s-aks-delete k8s-eks-render k8s-eks-apply k8s-eks-status k8s-eks-delete k8s-onprem-apply k8s-onprem-autoscaling-apply k8s-onprem-status k8s-onprem-delete k8s-smoke-test

VERSION    ?= $(shell ./scripts/resolve-version.sh 2>/dev/null || echo 0.1.0)
COMMIT     ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BI := github.com/jasonczech/apexload/pkg/buildinfo
LDFLAGS := -ldflags "-s -w -X $(BI).Version=$(VERSION) -X $(BI).Commit=$(COMMIT) -X $(BI).BuildTime=$(BUILD_TIME)"
GOFLAGS := -trimpath
# Leave discovery to build-desktop-local.sh when Wails is not on PATH; the
# helper also honors custom go env GOBIN/GOPATH installations.
WAILS ?= $(shell command -v wails 2>/dev/null)
WAILS_PLATFORM ?=
AKS_RENDERED_MANIFEST ?= /tmp/apexload-aks.yaml
EKS_RENDERED_MANIFEST ?= /tmp/apexload-eks.yaml

all: build

# Opt-in: `make build WITH_DESKTOP=1` also builds the host-platform desktop app.
# The default build stays pure-Go and zero-dependency.
ifeq ($(WITH_DESKTOP),1)
build: generator collector-agent desktop-api-binaries
else
build: generator controller apexctl recording-agent collector-agent
endif

generator:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/generator ./cmd/generator

controller:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/controller ./cmd/controller

apexctl:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexctl ./cmd/apexctl

recording-agent:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-record-agent ./cmd/apexload-record-agent

collector-agent:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-collector-agent ./cmd/apexload-collector-agent

package-recording-agent:
	./scripts/package-recording-agent.sh

desktop:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)" WAILS="$(WAILS)" WAILS_PLATFORM="$(WAILS_PLATFORM)" ./scripts/build-desktop-local.sh

desktop-release: desktop
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)" APEXLOAD_DESKTOP_PACKAGE_PLATFORM="$(WAILS_PLATFORM)" ./scripts/package-desktop.sh

# Every binary in this group imports pkg/api, whose embedded static assets are
# replaced by the desktop/Vite build. Keep those Go compilations behind desktop
# as a single recursive make edge so `make -j build WITH_DESKTOP=1` cannot read
# pkg/api/static while Vite is writing it.
desktop-api-binaries: desktop
	$(MAKE) --no-print-directory controller apexctl recording-agent VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)"

# clients builds the controller plus the HOST-platform Wails desktop app in one
# step, sharing VERSION/COMMIT/BUILD_TIME so both binaries carry identical
# buildinfo. Desktop apps CANNOT be cross-compiled to other OSes from a single
# machine (Wails links native webview libraries), so this builds only the
# current host's desktop app. For all-platform signed packages use the CI matrix
# in .github/workflows/desktop.yml.
clients: desktop
	$(MAKE) --no-print-directory controller VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)"
	@echo "Built bin/controller and the host-platform desktop app (desktop/build/bin/)."

# desktop-preflight fails early with actionable guidance when the desktop
# toolchain (wails CLI, node/npm, and on Linux the gtk/webkit2gtk dev headers)
# is missing, instead of surfacing a cryptic wails/npm error.
desktop-preflight:
	WAILS="$(WAILS)" WAILS_PLATFORM="$(WAILS_PLATFORM)" ./scripts/build-desktop-local.sh --check

# clients-release builds the controller plus a packaged host-platform desktop
# app (host platform only; signing/notarization still require the CI secrets).
clients-release: desktop-release
	$(MAKE) --no-print-directory controller VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)"
	@echo "Built bin/controller and a packaged host-platform desktop app (dist/desktop/)."

java-vuser-runner:
	mkdir -p bin/java_vuser
	rm -rf bin/java_vuser/classes bin/java_vuser/apexload-java-vuser-runner.jar
	javac --release 17 -d bin/java_vuser/classes runners/java_vuser/lrapi/lr.java runners/java_vuser/Harness.java
	jar --create --file bin/java_vuser/apexload-java-vuser-runner.jar --main-class Harness -C bin/java_vuser/classes .
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-java-vuser-runner ./cmd/apexload-java-vuser-runner

# java-vuser-runner-legacy builds the same harness targeted at Java 8 bytecode
# (class file major 52). The standard jar is compiled --release 17, so a Java 8
# JVM cannot load it at all — an operator running APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA
# against legacy middleware needs this jar, not the default one. It is built
# separately and never replaces the supported artifact.
java-vuser-runner-legacy:
	mkdir -p bin/java_vuser
	rm -rf bin/java_vuser/classes-java8 bin/java_vuser/apexload-java-vuser-runner-java8.jar
	javac --release 8 -nowarn -d bin/java_vuser/classes-java8 runners/java_vuser/lrapi/lr.java runners/java_vuser/Harness.java
	jar --create --file bin/java_vuser/apexload-java-vuser-runner-java8.jar --main-class Harness -C bin/java_vuser/classes-java8 .
	@echo "Built bin/java_vuser/apexload-java-vuser-runner-java8.jar (Java 8 bytecode)."
	@echo "Point APEXLOAD_JAVA_VUSER_HARNESS_JAR at it and set APEXLOAD_JAVA_VUSER_ALLOW_LEGACY_JAVA=true."

# Experimental slot artifacts are separate from the dedicated harness. Building
# these does not advertise pooled execution or change the default runner path.
.PHONY: java-vuser-slot-runtime
java-vuser-slot-runtime:
	mkdir -p bin/java_vuser/slot-api bin/java_vuser/slot-shim
	javac --release 8 -d bin/java_vuser/slot-api runners/java_vuser/apexload/bridge/SlotRuntime.java runners/java_vuser/apexload/host/SlotLoader.java runners/java_vuser/apexload/host/SlotWorker.java runners/java_vuser/apexload/host/PooledHost.java runners/java_vuser/apexload/host/PooledWire.java runners/java_vuser/apexload/host/PooledMain.java runners/java_vuser/apexload/host/GCPauses.java
	javac --release 8 -cp bin/java_vuser/slot-api -d bin/java_vuser/slot-shim runners/java_vuser/lrapi/lr.java runners/java_vuser/lrapi/SlotBridge.java
	jar --create --file bin/java_vuser/apexload-slot-api.jar -C bin/java_vuser/slot-api .
	jar --create --file bin/java_vuser/apexload-slot-shim.jar -C bin/java_vuser/slot-shim .

test:
	go test -race -count=1 ./...

test-jms-solace-real:
	go test -tags jms_solace_real ./pkg/protocol/jms

import-policy-locality:
	bash scripts/check-import-policy-locality.sh
	bash scripts/check-import-policy-locality-selftest.sh

# Safe local diagnostic: writes JSON to stdout and never creates an artifact.
performance-smoke:
	go run ./cmd/apexbench -profile smoke

# Publishable component artifact plumbing. Both paths must be explicit and the
# runner refuses to replace either existing file.
performance-evidence:
	test -n "$(PERF_JSON)" || (echo "PERF_JSON is required" && exit 1)
	test -n "$(PERF_MD)" || (echo "PERF_MD is required" && exit 1)
	./scripts/run-performance-evidence.sh "$(PERF_JSON)" "$(PERF_MD)"

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/proto/generator.proto

# Regenerate committed artifacts: DSL schema/reference (pkg/dsl) and the
# OpenAPI/Swagger spec (docs/swagger.yaml from the controller routing layer).
# Freshness is test-enforced (pkg/dsl/schemagen, pkg/api TestSwaggerYAMLIsFresh).
generate:
	go generate ./...

clean:
	rm -rf bin/ coverage.out coverage.html

docker-generator:
	docker build --build-arg BINARY=generator --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t apexload-generator:$(VERSION) -f Dockerfile .

docker-controller:
	docker build --build-arg BINARY=controller --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t apexload-controller:$(VERSION) -f Dockerfile .

docker-local-up:
	docker compose up --build -d

docker-local-down:
	docker compose down

docker-local-logs:
	docker compose logs -f controller prometheus

docker-local-data-up:
	docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml up --build -d

docker-local-data-down:
	docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml down

docker-local-data-logs:
	docker compose -f compose.yaml -f deployments/docker/compose.data-platform.yaml logs -f controller postgres redis prometheus

docker-local-prometheus-check:
	docker run --rm --entrypoint promtool -v "$(CURDIR)/deployments/docker/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro" prom/prometheus:v3.12.0 check config /etc/prometheus/prometheus.yml

# Build and push both images to any registry. Usage:
#   make docker-push REGISTRY=registry.example.internal/apexload
# For production supply-chain controls (SBOM, scan, portable signatures), use
# .github/workflows/release.yml and .github/workflows/publish-onprem.yml.
docker-push: docker-controller docker-generator
	test -n "$(REGISTRY)" || (echo "REGISTRY is required, e.g. make docker-push REGISTRY=registry.example.internal/apexload" && exit 1)
	docker tag apexload-controller:$(VERSION) $(REGISTRY)/apexload-controller:$(VERSION)
	docker tag apexload-generator:$(VERSION) $(REGISTRY)/apexload-generator:$(VERSION)
	docker push $(REGISTRY)/apexload-controller:$(VERSION)
	docker push $(REGISTRY)/apexload-generator:$(VERSION)

k8s-onprem-apply:
	kubectl apply -k deployments/k8s/onprem

k8s-onprem-autoscaling-apply:
	kubectl apply -k deployments/k8s/onprem-autoscaling

k8s-onprem-status:
	kubectl -n apexload get pods,svc,ingress,networkpolicy

k8s-onprem-delete:
	kubectl delete -k deployments/k8s/onprem --ignore-not-found=true

# Build in ACR and tag for AKS. Usage: make docker-push-acr ACR_NAME=myregistry
docker-push-acr:
	test -n "$(ACR_NAME)" || (echo "ACR_NAME is required, e.g. make docker-push-acr ACR_NAME=myregistry" && exit 1)
	az acr build --registry $(ACR_NAME) --image apexload-controller:$(VERSION) --build-arg BINARY=controller --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) --file Dockerfile .
	az acr build --registry $(ACR_NAME) --image apexload-generator:$(VERSION) --build-arg BINARY=generator --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) --file Dockerfile .

k8s-aks-render:
	./scripts/render-aks-manifests.sh --output "$(AKS_RENDERED_MANIFEST)" --image-tag "$(VERSION)"

k8s-aks-apply: k8s-aks-render
	kubectl apply -f "$(AKS_RENDERED_MANIFEST)"

k8s-aks-status:
	kubectl -n apexload get pods,svc,ingress,scaledobject

k8s-aks-delete:
	kubectl delete -k deployments/k8s/aks --ignore-not-found=true

k8s-eks-render:
	./scripts/render-eks-manifests.sh --output "$(EKS_RENDERED_MANIFEST)" --image-tag "$(VERSION)"

k8s-eks-apply: k8s-eks-render
	kubectl apply -f "$(EKS_RENDERED_MANIFEST)"

k8s-eks-status:
	kubectl -n apexload get pods,svc,ingress,scaledobject

k8s-eks-delete:
	kubectl delete -k deployments/k8s/eks --ignore-not-found=true

k8s-local-preflight:
	./deployments/k8s/local/preflight.sh

k8s-local-apply: k8s-local-preflight
	kubectl apply -k deployments/k8s/local

k8s-local-status:
	kubectl -n apexload-local get pods,svc,pvc

k8s-local-delete:
	kubectl delete -k deployments/k8s/local --ignore-not-found=true

k8s-kind-registry-setup:
	./deployments/k8s/local-kind/ensure-registry.sh

k8s-kind-push-images:
	./deployments/k8s/local-kind/push-images.sh

k8s-kind-apply: k8s-kind-registry-setup k8s-kind-push-images
	kubectl apply -k deployments/k8s/local-kind

k8s-kind-status:
	kubectl -n apexload-local get pods,svc,pvc

k8s-kind-delete:
	kubectl delete -k deployments/k8s/local-kind --ignore-not-found=true

k8s-smoke-test:
	./deployments/k8s/smoke-test.sh

# Experimental dedicated execution-pod supervisor; production construction is
# restricted to Linux PID 1. Local builds do not qualify container containment.
.PHONY: java-host-supervisor
java-host-supervisor:
	mkdir -p bin
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-java-host-supervisor ./cmd/apexload-java-host-supervisor

.PHONY: java-host-broker
java-host-broker:
	mkdir -p bin
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-java-host-broker ./cmd/apexload-java-host-broker

.PHONY: java-host-facade
java-host-facade:
	@mkdir -p bin
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexload-java-host-facade ./cmd/apexload-java-host-facade
