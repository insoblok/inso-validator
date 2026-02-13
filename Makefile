.PHONY: build run test lint clean init

BINARY := inso-validator
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

## build: Build the validator binary
build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/validator

## run: Build and run the validator
run: build
	./bin/$(BINARY) --config config.yaml

## init: Initialize validator keys and data directory
init: build
	./bin/$(BINARY) init --datadir ~/.inso-validator

## test: Run all tests
test:
	go test -race -count=1 ./...

## test-cover: Run tests with coverage
test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## lint: Run linter
lint:
	golangci-lint run ./...

## docker-build: Build Docker image
docker-build:
	docker build -t inso-validator:$(VERSION) .

## docker-run: Run in Docker
docker-run: docker-build
	docker run -v ~/.inso-validator:/data -p 30303:30303 -p 8547:8547 inso-validator:$(VERSION)

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
