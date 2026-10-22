GO ?= go
PYTHON ?= python3
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: build install test check test-local test-live test-linux test-web test-workloads test-gradle-services test-mesh test-mesh-ssh validate-knowledge
build: validate-knowledge
	CGO_ENABLED=0 $(GO) build -o bin/arm ./cmd/arm

validate-knowledge:
	GOOS= GOARCH= CGO_ENABLED=0 $(GO) run ./scripts/check-knowledge

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

# Opt-in real offline Cargo/rustc, Gradle/JDK and uv/Python scenarios.
# Missing toolchains fail; this target never downloads or installs them.
test-workloads: build
	python3 scripts/test-workloads.py

# Real owned persistent Gradle daemons/clients; requires installed Gradle/JDK.
# This never downloads tools or adopts a user daemon/cache.
test-gradle-services: build
	$(PYTHON) scripts/test-gradle-services.py --arm bin/arm

# Actual two-node mTLS plus strict TLS1.3 wire probes; no SSH/agent sign-in needed.
# PYTHON must have TLS1.3 support; missing probes fail this gate.
test-mesh: build
	$(PYTHON) scripts/test-mesh.py --arm bin/arm --require-tls-probes

# Explicit trusted SSH fixture and compatible destination binary; disposable homes only.
# Set MESH_SSH, MESH_REMOTE_ARM and MESH_REMOTE_ROOT (an existing private /tmp directory).
test-mesh-ssh: build
	$(PYTHON) scripts/test-mesh-lifecycle.py --arm bin/arm --ssh "$(MESH_SSH)" --remote-binary "$(MESH_REMOTE_ARM)" --remote-root "$(MESH_REMOTE_ROOT)"
	$(PYTHON) scripts/test-mesh-operator.py --arm bin/arm --ssh "$(MESH_SSH)" --remote-binary "$(MESH_REMOTE_ARM)" --remote-root "$(MESH_REMOTE_ROOT)"

test-local: build
	python3 scripts/test-local.py
	python3 scripts/test-ports.py
	python3 scripts/test-inbox.py
	python3 scripts/test-pools.py
	python3 scripts/test-subpools.py
	python3 scripts/test-usage.py
	python3 scripts/test-policy.py
	python3 scripts/test-terminal.py
	python3 scripts/test-monitor.py
	python3 scripts/test-dashboard.py
	python3 scripts/test-terminal-dashboard.py
	python3 scripts/test-terminal-pools.py
	python3 scripts/test-terminal-recovery.py
	python3 scripts/test-memory.py
	python3 scripts/test-live-disk.py
	python3 scripts/test-database.py
	python3 scripts/test-node-metadata.py
	mkdir -p .cache
	CGO_ENABLED=0 $(GO) build -o .cache/node-registry-probe ./scripts/node-registry-probe
	python3 scripts/test-node-registry.py --arm bin/arm --probe .cache/node-registry-probe
	python3 scripts/test-lifecycle.py
	python3 scripts/test-scheduling.py
	python3 scripts/test-budgets.py
	python3 scripts/test-agent-messages.py

# Select an already cached Linux image containing /bin/sh and python3.
test-linux:
	python3 scripts/test-linux.py --go $(GO) --image $(LINUX_IMAGE)
