.PHONY: all build test clean generator controller apexctl proto lint docker-generator docker-controller docker-push docker-push-acr k8s-local-preflight k8s-local-apply k8s-local-status k8s-local-delete k8s-kind-registry-setup k8s-kind-push-images k8s-kind-apply k8s-kind-status k8s-kind-delete k8s-aks-apply k8s-aks-status k8s-aks-delete k8s-onprem-apply k8s-onprem-status k8s-onprem-delete k8s-smoke-test

VERSION    ?= 0.1.0
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BI := github.com/jasonczech/apexload/pkg/buildinfo
LDFLAGS := -ldflags "-s -w -X $(BI).Version=$(VERSION) -X $(BI).Commit=$(COMMIT) -X $(BI).BuildTime=$(BUILD_TIME)"
GOFLAGS := -trimpath

all: build

build: generator controller apexctl

generator:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/generator ./cmd/generator

controller:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/controller ./cmd/controller

apexctl:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/apexctl ./cmd/apexctl

test:
	go test -race -count=1 ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/proto/generator.proto

clean:
	rm -rf bin/ coverage.out coverage.html

docker-generator:
	docker build --build-arg BINARY=generator --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t apexload-generator:$(VERSION) -f Dockerfile .

docker-controller:
	docker build --build-arg BINARY=controller --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) -t apexload-controller:$(VERSION) -f Dockerfile .

# Build and push both images to any registry. Usage:
#   make docker-push REGISTRY=registry.example.internal/apexload
# For supply-chain controls (SBOM, scan, sign), use the CI pipeline in
# deployments/ci/github-actions-onprem-registry.yml instead.
docker-push: docker-controller docker-generator
	test -n "$(REGISTRY)" || (echo "REGISTRY is required, e.g. make docker-push REGISTRY=registry.example.internal/apexload" && exit 1)
	docker tag apexload-controller:$(VERSION) $(REGISTRY)/apexload-controller:$(VERSION)
	docker tag apexload-generator:$(VERSION) $(REGISTRY)/apexload-generator:$(VERSION)
	docker push $(REGISTRY)/apexload-controller:$(VERSION)
	docker push $(REGISTRY)/apexload-generator:$(VERSION)

k8s-onprem-apply:
	kubectl apply -k deployments/k8s/onprem

k8s-onprem-status:
	kubectl -n apexload get pods,svc,ingress,scaledobject,networkpolicy

k8s-onprem-delete:
	kubectl delete -k deployments/k8s/onprem --ignore-not-found=true

# Build in ACR and tag for AKS. Usage: make docker-push-acr ACR_NAME=myregistry
docker-push-acr:
	test -n "$(ACR_NAME)" || (echo "ACR_NAME is required, e.g. make docker-push-acr ACR_NAME=myregistry" && exit 1)
	az acr build --registry $(ACR_NAME) --image apexload-controller:$(VERSION) --build-arg BINARY=controller --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) --file Dockerfile .
	az acr build --registry $(ACR_NAME) --image apexload-generator:$(VERSION) --build-arg BINARY=generator --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_TIME=$(BUILD_TIME) --file Dockerfile .

k8s-aks-apply:
	kubectl apply -k deployments/k8s/aks

k8s-aks-status:
	kubectl -n apexload get pods,svc,ingress,scaledobject

k8s-aks-delete:
	kubectl delete -k deployments/k8s/aks --ignore-not-found=true

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
