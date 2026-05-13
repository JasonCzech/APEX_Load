.PHONY: all build test clean generator controller stormctl proto lint

VERSION ?= 0.1.0
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
GOFLAGS := -trimpath

all: build

build: generator controller stormctl

generator:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/generator ./cmd/generator

controller:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/controller ./cmd/controller

stormctl:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/stormctl ./cmd/stormctl

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
	docker build -t stormrunner-generator:$(VERSION) -f deployments/docker/Dockerfile.generator .

docker-controller:
	docker build -t stormrunner-controller:$(VERSION) -f deployments/docker/Dockerfile.controller .
