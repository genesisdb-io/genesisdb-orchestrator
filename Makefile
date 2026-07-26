# Local builds use 0.0.0. GoReleaser overrides VERSION from the release tag.
BINARY := genesisdb
VERSION ?= 0.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test lint fmt clean release

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/genesisdb

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/genesisdb

test:
	go test -race ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt issues"; exit 1)

fmt:
	gofmt -w cmd internal

release:
	@mkdir -p bin
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 ./cmd/genesisdb
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 ./cmd/genesisdb
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/genesisdb
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/genesisdb
	GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-arm64.exe ./cmd/genesisdb
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/genesisdb

clean:
	rm -rf bin dist
