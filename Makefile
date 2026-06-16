BINARY_NAME := stepi
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X main.version=$(VERSION)
BUILD_FLAGS := -trimpath -ldflags="$(LDFLAGS)"
DIST_DIR    := dist

.DEFAULT_GOAL := build

.PHONY: build install clean dist test vet help

## build: Build the stepi binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_NAME) .

## install: Install stepi to ~/.local/bin
install: build
	@echo "Installing $(BINARY_NAME) to ~/.local/bin/..."
	@mkdir -p ~/.local/bin
	@cp $(BINARY_NAME) ~/.local/bin/$(BINARY_NAME)
	@echo "Done. Make sure ~/.local/bin is in your PATH."

## dist: Cross-compile release binaries for all platforms into ./dist/
dist:
	@echo "Building release binaries for version $(VERSION)..."
	@mkdir -p $(DIST_DIR)
	@set -e; \
	for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		GOOS=$$(echo $$platform | cut -d/ -f1); \
		GOARCH=$$(echo $$platform | cut -d/ -f2); \
		SUFFIX=""; [ "$$GOOS" = "windows" ] && SUFFIX=".exe"; \
		echo "  → $$GOOS/$$GOARCH"; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH \
			go build $(BUILD_FLAGS) \
			-o $(DIST_DIR)/$(BINARY_NAME)-$(VERSION)-$$GOOS-$$GOARCH$$SUFFIX .; \
	done
	@cd $(DIST_DIR) && sha256sum * > checksums.txt
	@echo "" && echo "Binaries in $(DIST_DIR)/" && ls -lh $(DIST_DIR)/

## test: Run tests
test:
	@go test -v -race -count=1 ./...

## vet: Run go vet
vet:
	@go vet ./...

## clean: Remove built binaries and dist/
clean:
	@rm -f $(BINARY_NAME)
	@rm -rf $(DIST_DIR)/

## help: Show this help
help:
	@echo "stepi build targets:"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
	@echo ""
	@echo "  VERSION=$(VERSION)"
