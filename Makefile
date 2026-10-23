GO ?= go
PYTHON ?= python3
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
BUNDLE_DIR ?= dist
BUNDLE_JOBS ?= 0

.PHONY: build install test check test-local test-live test-linux test-web test-workloads test-cargo-profile test-gradle-services test-mesh test-mesh-ssh test-ssh-history test-database-reset test-workspace-lifecycle test-workspaces test-workspace-routing test-workspace-graph test-published-agents validate-knowledge
build: validate-knowledge
	CGO_ENABLED=0 $(GO) build -o bin/arm ./cmd/arm

validate-knowledge:
	GOOS= GOARCH= CGO_ENABLED=0 $(GO) run ./scripts/check-knowledge

# Install the self-contained carrier; a fresh private output makes repeated
# installs independent of dist/ and preserves an existing install on build failure.
install: validate-knowledge
	@set -eu; \
	arm_install_build=$$(mktemp -d "$${TMPDIR:-/tmp}/arm-install.XXXXXXXXXX"); \
	trap 'rm -rf -- "$$arm_install_build"' 0; \
	GOOS= GOARCH= CGO_ENABLED=0 "$(GO)" run ./scripts/build-bundle --carrier --jobs "$(BUNDLE_JOBS)" --go "$(GO)" --output "$$arm_install_build"; \
	install -d "$(DESTDIR)$(BINDIR)"; \
	install -m 755 "$$arm_install_build/arm" "$(DESTDIR)$(BINDIR)/arm"

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

# Explicit concrete installed tools make real offline Cargo tests mandatory.
# These tests never install toolchains or use the caller's Cargo home/cache.
test-cargo-profile:
	@test -n "$$ARM_TEST_CARGO" && test -n "$$ARM_TEST_RUSTC" && test -n "$$ARM_TEST_RUSTDOC" || { echo 'Set absolute ARM_TEST_CARGO, ARM_TEST_RUSTC and ARM_TEST_RUSTDOC paths to installed tools.' >&2; exit 1; }
	$(GO) test -race ./internal/cargoprofile

# Real owned persistent Gradle daemons/clients; requires installed Gradle/JDK.
# This never downloads tools or adopts a user daemon/cache.
test-gradle-services: build
	$(PYTHON) scripts/test-gradle-services.py --arm bin/arm

# Destructive-operation acceptance uses only disposable private databases.
test-database-reset: build
	$(PYTHON) scripts/test-database-reset.py --arm bin/arm

# Actual two-node mTLS plus strict TLS1.3 wire probes; no SSH/agent sign-in needed.
# PYTHON must have TLS1.3 support; missing probes fail this gate.
test-mesh: build
	$(PYTHON) scripts/test-mesh.py --arm bin/arm --require-tls-probes

# Actual OpenSSH, reviewed archival/recovery and permanent old-key fences.
# Requires installed ssh/openssl and Python with TLS1.3; never installs tools.
test-ssh-history: build
	$(PYTHON) scripts/test-ssh-history.py --arm bin/arm

# Explicit trusted SSH fixture and compatible destination binary; disposable homes only.
# Set MESH_SSH, MESH_REMOTE_ARM and MESH_REMOTE_ROOT (an existing private /tmp directory).
test-mesh-ssh: build
	$(PYTHON) scripts/test-mesh-lifecycle.py --arm bin/arm --ssh "$(MESH_SSH)" --remote-binary "$(MESH_REMOTE_ARM)" --remote-root "$(MESH_REMOTE_ROOT)"
	$(PYTHON) scripts/test-mesh-operator.py --arm bin/arm --ssh "$(MESH_SSH)" --remote-binary "$(MESH_REMOTE_ARM)" --remote-root "$(MESH_REMOTE_ROOT)"

# Private compiled crash/restart/ordinary cleanup with optional workspace corruption.
test-workspace-lifecycle: build
	mkdir -p .cache
	GOOS= GOARCH= CGO_ENABLED=0 $(GO) test -c -o .cache/workspace-manager.test ./internal/manager
	$(PYTHON) scripts/test-workspace-lifecycle.py --arm bin/arm --manager-tests .cache/workspace-manager.test

# Real three-daemon reviewed workspace proof/directory/recovery; no routed message claim.
test-workspaces: build
	$(PYTHON) scripts/test-workspaces.py --arm bin/arm

# Real three-daemon directory-relayed host inbox/replies, exact dedup and lost-ACK recovery.
# No direct origin-recipient enrollment or general graph-discovery claim.
test-workspace-routing: build
	$(PYTHON) scripts/test-workspace-routing.py --arm bin/arm

# Actual four-daemon graph with revoked bootstrap edge and alternate relay.
test-workspace-graph: build
	$(PYTHON) scripts/test-workspace-graph.py --arm bin/arm

# Actual three-daemon explicitly published poll-agent send/reply and lost-ACK recovery.
test-published-agents: build
	$(PYTHON) scripts/test-published-agents.py --arm bin/arm

test-local: build test-workspace-lifecycle test-workspaces test-workspace-routing test-workspace-graph test-ssh-history test-published-agents
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
	python3 scripts/test-terminal-colors.py
	python3 scripts/test-terminal-pools.py
	python3 scripts/test-terminal-recovery.py
	$(PYTHON) scripts/test-terminal-mesh.py --arm bin/arm
	python3 scripts/test-memory.py
	python3 scripts/test-live-disk.py
	python3 scripts/test-database.py
	python3 scripts/test-database-reset.py
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

# Explicit distribution build: four thin platform payloads, then one native
# carrier containing their compressed bytes. Normal make build stays thin.
.PHONY: bundle carrier bundle-archive
bundle carrier: validate-knowledge
	GOOS= GOARCH= CGO_ENABLED=0 $(GO) run ./scripts/build-bundle --carrier --jobs "$(BUNDLE_JOBS)" --go "$(GO)" --output "$(BUNDLE_DIR)"

# Optional extracted companion layout for development/distribution inspection.
bundle-archive: validate-knowledge
	GOOS= GOARCH= CGO_ENABLED=0 $(GO) run ./scripts/build-bundle --jobs "$(BUNDLE_JOBS)" --go "$(GO)" --output "$(BUNDLE_DIR)"

# Development-only formal tools. Downloads/executables, TLC states/traces and
# TLAPM caches stay in gitignored .tla/. Normal ARM builds require none of them.
JAVA ?= java
TLC_JAR ?= $(CURDIR)/.tla/tools/tlc/tla2tools.jar
TLAPM ?= $(CURDIR)/.tla/tools/tlapm/tlapm/bin/tlapm
.PHONY: tla-tools tla-tlc tla-tlapm tla-tool-check
tla-tools:
	$(PYTHON) scripts/provision-tla.py --component all

tla-tlc:
	$(PYTHON) scripts/provision-tla.py --component tlc

tla-tlapm:
	$(PYTHON) scripts/provision-tla.py --component tlapm

# Actual strict SMT proof health check, separate from ARM model verification.
# A missing/broken tool or unproved obligation fails; it is never a skip.
tla-tool-check: tla-tools
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/test-provision-tla.py
	mkdir -p .tla/proofs/toolchain .tla/states/toolchain .tla/traces/toolchain .tla/tmp/toolchain
	cp formal/ToolchainSmoke.tla .tla/proofs/toolchain/ToolchainSmoke.tla
	"$(JAVA)" -Djava.io.tmpdir="$(CURDIR)/.tla/tmp/toolchain" -Xmx512m -cp "$(TLC_JAR)" tlc2.TLC -workers 1 -noGenerateSpecTE -metadir .tla/states/toolchain -teSpecOutDir .tla/traces/toolchain -config formal/ToolchainCounter.cfg formal/ToolchainCounter.tla
	"$(TLAPM)" --strict --nofp --threads 2 --cache-dir .tla/proofs/toolchain/cache .tla/proofs/toolchain/ToolchainSmoke.tla

# ARM protocol models, implementation trace comparison and checked proofs.
# Explicit modes support shorter local iterations; test-formal runs every gate.
TLA_WORK_DIR ?= $(CURDIR)/.tla/share-preparation
TLA_AGENT_WORK_DIR ?= $(CURDIR)/.tla/published-agents
TLA_PUBLICATION_WORK_DIR ?= $(CURDIR)/.tla/share-publication
.PHONY: tla-check tla-traces tla-prove test-formal
tla-check: tla-tlc
	$(PYTHON) scripts/test-formal.py --mode check --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --work-dir "$(TLA_WORK_DIR)"
	$(PYTHON) scripts/test-formal-publication.py --mode check --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --work-dir "$(TLA_PUBLICATION_WORK_DIR)"
	$(PYTHON) scripts/test-published-agents-formal.py --mode check --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --work-dir "$(TLA_AGENT_WORK_DIR)"

tla-traces: tla-tlc
	$(PYTHON) scripts/test-formal.py --mode traces --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --go "$(GO)" --work-dir "$(TLA_WORK_DIR)"
	$(PYTHON) scripts/test-formal-publication.py --mode traces --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --go "$(GO)" --work-dir "$(TLA_PUBLICATION_WORK_DIR)"
	$(PYTHON) scripts/test-published-agents-formal.py --mode traces --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --go "$(GO)" --work-dir "$(TLA_AGENT_WORK_DIR)"

tla-prove: tla-tlapm
	$(PYTHON) scripts/test-formal.py --mode prove --tlapm "$(TLAPM)" --work-dir "$(TLA_WORK_DIR)"
	$(PYTHON) scripts/test-formal-publication.py --mode prove --tlapm "$(TLAPM)" --work-dir "$(TLA_PUBLICATION_WORK_DIR)"
	$(PYTHON) scripts/test-published-agents-formal.py --mode prove --tlapm "$(TLAPM)" --work-dir "$(TLA_AGENT_WORK_DIR)"

test-formal: tla-tools
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/test-formal-runner.py
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/test-formal-publication-runner.py
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/test-published-agents-formal-runner.py
	$(PYTHON) scripts/test-formal.py --mode all --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --tlapm "$(TLAPM)" --go "$(GO)" --work-dir "$(TLA_WORK_DIR)"
	$(PYTHON) scripts/test-formal-publication.py --mode all --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --tlapm "$(TLAPM)" --go "$(GO)" --work-dir "$(TLA_PUBLICATION_WORK_DIR)"
	$(PYTHON) scripts/test-published-agents-formal.py --mode all --java "$(JAVA)" --tlc-jar "$(TLC_JAR)" --tlapm "$(TLAPM)" --go "$(GO)" --work-dir "$(TLA_AGENT_WORK_DIR)"
