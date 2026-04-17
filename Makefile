# filmkit-daemon build system
# Cross-compiles for GL.inet GL-BE9300 (aarch64_cortex-a53) and GL-E5800 (aarch64)

BINARY    := filmkit-daemon
CMD       := ./cmd/filmkit-daemon
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -ldflags "-X main.version=$(VERSION) -s -w"

# Target architecture
GOARCH    := arm64
GOOS      := linux
# CGO cross-compiler — install with: make install-cross
CC        := aarch64-unknown-linux-musl-gcc

# Router connection (for deploy target)
ROUTER_IP   ?= 10.0.1.1
ROUTER_USER ?= root

.PHONY: build build-local clean deploy install-cross deps

# Static libusb path (cross-compiled ARM64 build)
LIBUSB_DIR ?= /tmp/libusb-arm64
CGO_CFLAGS  := -I$(LIBUSB_DIR)/include/libusb-1.0
CGO_LDFLAGS := -L$(LIBUSB_DIR)/lib -lusb-1.0 -static

## Build daemon for router (arm64 Linux, requires cross-compiler + static libusb)
build:
	mkdir -p dist
	CGO_ENABLED=1 \
	GOOS=$(GOOS) \
	GOARCH=$(GOARCH) \
	CC=$(CC) \
	CGO_CFLAGS="$(CGO_CFLAGS)" \
	CGO_LDFLAGS="$(CGO_LDFLAGS)" \
	go build $(LDFLAGS) -o dist/$(BINARY) $(CMD)
	@echo "Built: dist/$(BINARY) ($(GOOS)/$(GOARCH))"

## Build for local machine (macOS/Linux, for testing)
build-local:
	CGO_ENABLED=1 \
	go build $(LDFLAGS) -o dist/$(BINARY)-local $(CMD)
	@echo "Built: dist/$(BINARY)-local (local)"

## Download Go dependencies
deps:
	go mod tidy
	go mod download

## Deploy daemon binary + init script to router via SSH
## To also deploy the frontend, use the filmkit-glinet integration repo.
deploy: build
	@echo "Deploying daemon to $(ROUTER_USER)@$(ROUTER_IP)..."
	ssh $(ROUTER_USER)@$(ROUTER_IP) "mkdir -p /usr/bin /etc/init.d"
	cat dist/$(BINARY) | ssh $(ROUTER_USER)@$(ROUTER_IP) "cat > /usr/bin/$(BINARY) && chmod +x /usr/bin/$(BINARY)"
	cat openwrt/files/etc/init.d/filmkit | ssh $(ROUTER_USER)@$(ROUTER_IP) "cat > /etc/init.d/filmkit && chmod +x /etc/init.d/filmkit"
	ssh $(ROUTER_USER)@$(ROUTER_IP) "/etc/init.d/filmkit restart"
	@echo "Daemon deployed. Use filmkit-glinet to also deploy the frontend."

## Install cross-compiler on macOS (homebrew)
install-cross:
	brew tap messense/macos-cross-toolchains
	brew install aarch64-unknown-linux-musl

## Remove build artifacts
clean:
	rm -rf dist/
