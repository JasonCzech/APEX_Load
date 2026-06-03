.PHONY: all build test clean generator controller apexctl proto lint docker-generator docker-controller k8s-local-preflight k8s-local-apply k8s-local-status k8s-local-delete k8s-kind-registry-setup k8s-kind-push-images k8s-kind-apply k8s-kind-status k8s-kind-delete k8s-smoke-test

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
