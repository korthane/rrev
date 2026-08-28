BINARY := rrev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build test lint coverage clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/rrev

test:
	go test -race ./...

lint:
	golangci-lint run ./...

coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

clean:
	rm -f $(BINARY) coverage.out
