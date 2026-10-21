GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: build install test check test-local test-live test-linux test-web
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

# Development-only pure JavaScript checks. ARM build/runtime needs no Node.js.
test-web:
	node --check internal/server/web/app.mjs
	node --test internal/server/web/*.test.mjs

test-live: build
	CGO_ENABLED=0 $(GO) build -o .cache/wsbridge ./scripts/wsbridge
	python3 scripts/test-live-agents.py

test-local: build
	python3 scripts/test-local.py
	python3 scripts/test-ports.py
	python3 scripts/test-inbox.py
	python3 scripts/test-pools.py
	python3 scripts/test-usage.py
	python3 scripts/test-policy.py
	python3 scripts/test-terminal.py
	python3 scripts/test-monitor.py
	python3 scripts/test-dashboard.py

# Select an already cached Linux image containing /bin/sh and python3.
test-linux:
	python3 scripts/test-linux.py --go $(GO) --image $(LINUX_IMAGE)
