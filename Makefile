BUILD_DIR = build

# Get the current Git hash
GIT_HASH := $(shell git rev-parse --short HEAD)
ifneq ($(shell git status --porcelain),)
    # There are untracked changes
    GIT_HASH := $(GIT_HASH)+
endif

# Capture the current build date in RFC3339 format
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")


all: examples binaries

binaries: ts-plug ts-unplug ts-unplug-proxy

ts-plug:
	go build -o build/ts-plug ./cmd/ts-multi-plug

ts-unplug:
	go build -o build/ts-unplug ./cmd/ts-unplug

ts-unplug-proxy:
	go build -o build/ts-unplug-proxy ./cmd/ts-unplug-proxy

darwin: darwin-ts-plug darwin-ts-unplug darwin-ts-unplug-proxy

darwin-ts-plug:
	GOOS=darwin GOARCH=arm64 go build -o build/ts-plug-darwin-arm64 ./cmd/ts-multi-plug

darwin-ts-unplug:
	GOOS=darwin GOARCH=arm64 go build -o build/ts-unplug-darwin-arm64 ./cmd/ts-unplug

darwin-ts-unplug-proxy:
	GOOS=darwin GOARCH=arm64 go build -o build/ts-unplug-proxy-darwin-arm64 ./cmd/ts-unplug-proxy

linux: linux-ts-plug linux-ts-unplug linux-ts-unplug-proxy

linux-ts-plug:
	GOOS=linux GOARCH=arm64 go build -o build/ts-plug-linux-arm64 ./cmd/ts-multi-plug
	GOOS=linux GOARCH=amd64 go build -o build/ts-plug-linux-amd64 ./cmd/ts-multi-plug

linux-ts-unplug:
	GOOS=linux GOARCH=arm64 go build -o build/ts-unplug-linux-arm64 ./cmd/ts-unplug
	GOOS=linux GOARCH=amd64 go build -o build/ts-unplug-linux-amd64 ./cmd/ts-unplug

linux-ts-unplug-proxy:
	GOOS=linux GOARCH=arm64 go build -o build/ts-unplug-proxy-linux-arm64 ./cmd/ts-unplug-proxy
	GOOS=linux GOARCH=amd64 go build -o build/ts-unplug-proxy-linux-amd64 ./cmd/ts-unplug-proxy

install: binaries
	cp build/ts-plug $(GOPATH)/bin/ts-plug
	cp build/ts-unplug $(GOPATH)/bin/ts-unplug
	cp build/ts-unplug-proxy $(GOPATH)/bin/ts-unplug-proxy

clean:
	rm -rf $(BUILD_DIR)/*

examples:
	go build -o $(BUILD_DIR)/hello ./cmd/examples/hello/hello.go
	go build -o $(BUILD_DIR)/resolver ./cmd/examples/resolver/resolver.go

# use cached test results while developing
test: examples
#	go test -race -timeout 30s -short ./internal/...
	staticcheck ./... || true

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

.PHONY: all test examples clean binaries ts-plug ts-unplug ts-unplug-proxy darwin darwin-ts-plug darwin-ts-unplug darwin-ts-unplug-proxy linux linux-ts-plug linux-ts-unplug linux-ts-unplug-proxy install
