GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: build install test check test-local test-live test-linux
build:
	CGO_ENABLED=0 $(GO) build -o bin/arm ./cmd/arm

install: build
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 755 bin/arm "$(DESTDIR)$(BINDIR)/arm"

test:
	$(GO) test ./...

check:
	$(GO) vet ./...
	$(GO) test -race ./...

test-live: build
	CGO_ENABLED=0 $(GO) build -o .cache/wsbridge ./scripts/wsbridge
	python3 scripts/test-live-agents.py

test-local: build
	python3 scripts/test-local.py
	python3 scripts/test-ports.py
	python3 scripts/test-inbox.py

# Select an already cached Linux image containing /bin/sh and python3.
test-linux:
	python3 scripts/test-linux.py --go $(GO) --image $(LINUX_IMAGE)
