.PHONY: build test lint clean

VERSION ?= $(shell git describe --tags --always --dirty)

build:
	go build -ldflags "-X github.com/ggwhite/memory-mcp/internal/cli.version=$(VERSION)" -o bin/memory-mcp .

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
