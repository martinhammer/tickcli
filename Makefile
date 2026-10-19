# SPDX-License-Identifier: GPL-3.0-or-later

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/martinhammer/tickcli/internal/cli.version=$(VERSION)
STATICCHECK ?= go run honnef.co/go/tools/cmd/staticcheck@latest
TARGETS := linux/amd64 linux/arm64 linux/arm/7 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build test lint release-local release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o tickcli ./cmd/tickcli

test:
	go test ./...

lint:
	go vet ./...
	$(STATICCHECK) ./...

# Static binaries for every target in dist/.
release-local:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$$(echo $$t | cut -d/ -f1); arch=$$(echo $$t | cut -d/ -f2); arm=$$(echo $$t | cut -d/ -f3); \
		name=tickcli-$(VERSION)-$$os-$$arch$${arm:+v$$arm}; [ $$os = windows ] && name=$$name.exe; \
		echo "dist/$$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$arm go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$name ./cmd/tickcli || exit 1; \
	done

# Release downloads in dist/ for the tag on this commit (scripts/release.sh).
# VERSION=… on the command line must equal that tag.
release:
	@sh scripts/release.sh $(if $(filter command line,$(origin VERSION)),$(VERSION))

clean:
	rm -rf dist tickcli
