MODULE  := github.com/rmto/raspberryrelay
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION) -s -w
BUILD   := build

.PHONY: all build test vet run clean arm64 arm7 linux-amd64 cross

all: test build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD)/relayd ./cmd/relayd

test:
	go test ./... -race

vet:
	go vet ./...

run:
	RELAYD_GPIO=mock go run ./cmd/relayd -config /tmp/relayd-dev/config.json -listen :8080

clean:
	rm -rf $(BUILD)

# Raspberry Pi 3/4/5 running a 64-bit OS.
arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/relayd-arm64 ./cmd/relayd

# Raspberry Pi Zero 2 / older 32-bit Raspberry Pi OS.
arm7:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/relayd-armv7 ./cmd/relayd

linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD)/relayd-amd64 ./cmd/relayd

cross: arm64 arm7 linux-amd64
	@echo "built $(VERSION):"
	@ls -la $(BUILD)
