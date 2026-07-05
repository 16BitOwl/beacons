BINARY     := beacons
IMAGE      := beacons
VERSION    ?= dev
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build docker run fmt vet lint test tidy clean

## build: compile for Linux (the only supported target)
build:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)" -o $(BINARY) ./cmd/beacons

## docker: build the Docker image
docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg BUILDTIME=$(BUILD_TIME) -t $(IMAGE):$(VERSION) .

## run: run via Docker Compose
run:
	docker compose up --build

## fmt: format all Go source
fmt:
	go fmt ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint (must be installed)
lint:
	golangci-lint run ./...

## test: run all tests
test:
	go test ./...

## tidy: tidy go.mod and go.sum
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -f $(BINARY)
