BINARY := lsy
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test vet fmt lint clean install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/lsy

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/lsy

test:
	go test -race -v -cover ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l .)" || (echo "Files need gofmt:"; gofmt -l .; exit 1)
	go vet ./...
	sh -n install.sh

clean:
	rm -rf bin dist
