.PHONY: build test lint clean

build:
	go build -o bin/memory-mcp .

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
