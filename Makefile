.PHONY: all build static test test-v test-worker test-shellcheck test-unit test-adversarial test-mutation test-mutation-analysis test-race test-bench test-integration test-live-portage-compile test-coverage test-coverage-network test-coverage-benchmark test-vendor-artifact audit-repo vet lint clean install uninstall man info bench bench-quick bench-compare bench-json perf-harness perf-table perf-prepare perf-smoke check-release-version deps deps-cache

BINARY := arise
MODULE := github.com/airencracken/arise
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man
INFODIR ?= $(PREFIX)/share/info
DOCDIR ?= $(PREFIX)/share/doc/arise

GO ?= go
GOFLAGS ?= -buildvcs=false -trimpath -ldflags="-s -w"
COVERAGE_CORE_PKGS := $(shell $(GO) list ./cmd/... ./internal/... | grep -Ev '/(benchmark|binpkg|fetch|integration)$$')

all: build test vet

build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/arise/
	@file $(BINARY) | grep -q "statically linked" || { file $(BINARY); echo "static build: FAILED" >&2; exit 1; }
	@echo "static build: OK"

static: build

#
# Tests
#

test: test-worker
	$(GO) test ./... -count=1 -timeout 120s

test-v: test-worker
	$(GO) test ./... -v -count=1 -timeout 120s

test-worker:
	bash -n internal/phaseproto/worker.sh

# Optional richer analysis; unlike test-worker, this requires shellcheck.
test-shellcheck:
	shellcheck -s bash internal/phaseproto/worker.sh

test-unit:
	$(GO) test $$($(GO) list ./internal/... | grep -v /integration$$) -run 'Test[^P]|TestP[a-ln-z]' -count=1 -timeout 60s

test-adversarial:
	$(GO) test ./internal/... -run 'Adversar|Mutation' -count=1 -timeout 60s

test-mutation:
	$(GO) test ./internal/... -run 'Mutation' -count=1 -timeout 60s

# Real source mutation analysis is deliberately local and narrowly targeted.
# Install the pinned runner documented in docs/testing/COVERAGE.md first.
MUTATION_TOOL ?= go-mutesting
MUTATION_TARGETS ?= internal/log
MUTATION_MATCH ?=
test-mutation-analysis:
	@command -v $(MUTATION_TOOL) >/dev/null 2>&1 || { echo "$(MUTATION_TOOL) is required; see docs/testing/COVERAGE.md" >&2; exit 1; }
	$(MUTATION_TOOL) --coverage --per-test --quiet --no-diffs --logger-summary-json --exec-timeout=20 $(if $(MUTATION_MATCH),--match='$(MUTATION_MATCH)',) $(MUTATION_TARGETS)

test-race:
	$(GO) test ./internal/... -race -count=1 -timeout 300s

test-integration:
	@if [ -d /var/db/repos/gentoo/metadata/md5-cache ]; then \
		echo "Running integration tests against live Gentoo tree..."; \
		$(GO) test -tags=live_portage ./internal/integration/ ./internal/phaseproto/ ./internal/rebuild/ -count=1 -v -timeout 10m; \
	else \
		echo "No Gentoo tree found. Skipping."; \
	fi

test-live-portage-compile:
	$(GO) test -tags=live_portage ./internal/integration ./internal/benchmark ./internal/phaseproto ./internal/rebuild -run '^$$' -count=1

PORTAGE_REPO ?= /var/db/repos/gentoo
AUDIT_OUTPUT ?= /tmp/arise-repository-compatibility.json

audit-repo:
	$(GO) run ./cmd/arise-repo-audit -repo $(PORTAGE_REPO) -worker internal/phaseproto/worker.sh -output $(AUDIT_OUTPUT)

test-coverage:
	$(GO) test $(COVERAGE_CORE_PKGS) -coverpkg=./... -coverprofile=/tmp/arise-coverage.out -covermode=atomic -count=1 -timeout 60s
	$(GO) tool cover -func=/tmp/arise-coverage.out > /tmp/arise-coverage-functions.txt
	@tail -n 1 /tmp/arise-coverage-functions.txt
	@echo "Function report: /tmp/arise-coverage-functions.txt"
	@echo "Core coverage excludes network-listener, live-integration and benchmark test execution; all production packages remain instrumented."

test-coverage-network:
	$(GO) test ./internal/binpkg ./internal/fetch -coverpkg=./... -coverprofile=/tmp/arise-coverage-network.out -covermode=atomic -count=1 -timeout 60s

test-coverage-benchmark:
	$(GO) test ./internal/benchmark -coverpkg=./... -coverprofile=/tmp/arise-coverage-benchmark.out -covermode=atomic -count=1 -timeout 180s
	$(GO) tool cover -func=/tmp/arise-coverage-benchmark.out > /tmp/arise-coverage-benchmark-functions.txt
	@tail -n 1 /tmp/arise-coverage-benchmark-functions.txt
	@echo "Benchmark function report: /tmp/arise-coverage-benchmark-functions.txt"

test-coverage-html: test-coverage
	$(GO) tool cover -html=/tmp/arise-coverage.out -o /tmp/arise-coverage.html
	@echo "Coverage report: /tmp/arise-coverage.html"

#
# Benchmarks
#

bench:
	$(GO) test ./internal/benchmark/ -bench=. -benchtime=1s -count=1 -timeout 10m

bench-quick:
	$(GO) test ./internal/benchmark/ -bench=. -benchtime=100ms -count=1 -timeout 5m

bench-compare:
	$(GO) test -tags=live_portage ./internal/benchmark/ -run 'TestCompare' -v -count=1 -timeout 10m

bench-json:
	./$(BINARY) bench --json 2>/dev/null || $(GO) test ./internal/benchmark/ -bench=. -benchtime=1s

bench-all: bench bench-compare

perf-harness:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o arise-perf ./cmd/arise-perf/
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o arise-perf-table ./cmd/arise-perf-table/

perf-table: perf-harness
	@test -n "$(REPORTS)" || { echo "REPORTS='/tmp/report1.json /tmp/report2.json' is required"; exit 1; }
	./arise-perf-table $(REPORTS)

perf-prepare: build
	./arise --db /tmp/arise-perf-data --repo /var/db/repos/gentoo index

perf-smoke: build perf-harness
	./arise-perf -workload misc/perf-smoke.json -snapshot smoke -output /tmp/arise-perf-smoke.json
	@echo "Performance report: /tmp/arise-perf-smoke.json"

#
# Quality
#

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./... 2>/dev/null || echo "golangci-lint not installed, skipping"

#
# Lifecycle
#

clean:
	rm -f $(BINARY) arise-perf arise-perf-table arise.info
	rm -f /tmp/arise-coverage.out /tmp/arise-coverage.html
	$(GO) clean -testcache

BASHCOMPDIR ?= $(PREFIX)/share/bash-completion/completions

install: build info
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/
	install -d $(DESTDIR)$(MANDIR)/man1
	install -m 644 arise.1 $(DESTDIR)$(MANDIR)/man1/
	install -d $(DESTDIR)$(INFODIR)
	install -m 644 arise.info $(DESTDIR)$(INFODIR)/
	install -d $(DESTDIR)$(DOCDIR)
	install -m 644 README.md LICENSE $(DESTDIR)$(DOCDIR)/
	install -d $(DESTDIR)$(BASHCOMPDIR)
	install -m 644 misc/arise-completion.bash $(DESTDIR)$(BASHCOMPDIR)/arise

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(MANDIR)/man1/arise.1
	rm -f $(DESTDIR)$(INFODIR)/arise.info
	rm -f $(DESTDIR)$(BASHCOMPDIR)/arise

#
# Docs
#

man:
	./$(BINARY) --help 2>&1 || $(GO) run ./cmd/arise/ --help 2>&1 || true
	@echo "Man page: see arise.1"

info: arise.info
	@echo "Info page: arise.info"

arise.info: arise.texi
	@command -v makeinfo >/dev/null 2>&1 || { echo "makeinfo is required to build arise.info" >&2; exit 1; }
	makeinfo --no-split -o $@ $<

docs: man info
	@echo "Documentation built."

PROJECT_VERSION := 0.0.22
VERSION ?= $(PROJECT_VERSION)
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct)
check-release-version:
	@test "$(VERSION)" = "$(PROJECT_VERSION)" || { \
		echo "VERSION=$(VERSION) does not match source release version $(PROJECT_VERSION)" >&2; \
		exit 1; \
	}

#
# Go module management. Release builds use a deterministic vendor archive so
# the repository stays unvendored while Portage builds remain network-free.
#
# For emerge builds, publish the archive produced by `make deps VERSION=x.y.z`.
#
# For development:
#   go mod download        # fetch deps from proxy
#   go mod verify          # verify go.sum integrity
#
download:
	$(GO) mod download
	$(GO) mod verify
	@echo "All module dependencies downloaded and verified."

deps: check-release-version
	VERSION="$(VERSION)" SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" bash scripts/build-vendor-artifact.sh

test-vendor-artifact: check-release-version
	VERSION="$(VERSION)" SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" bash scripts/test-vendor-artifact.sh

# Retained only to reproduce already-published releases. New releases use deps.
deps-cache: check-release-version
	@test -n "$(VERSION)" || { echo "VERSION is required"; exit 1; }
	@test -n "$(SOURCE_DATE_EPOCH)" || { echo "SOURCE_DATE_EPOCH is required"; exit 1; }
	mkdir -p dist
	sha256sum go.mod go.sum > dist/.go-module-input.sha256
	rm -rf dist/go-mod
	mkdir -p dist/go-mod
	GOMODCACHE="$(CURDIR)/dist/go-mod" $(GO) mod download -modcacherw all
	GOMODCACHE="$(CURDIR)/dist/go-mod" GOPROXY=off $(GO) mod verify
	@sha256sum --check --status dist/.go-module-input.sha256 || { \
		echo "go mod download changed go.mod/go.sum; review and commit those changes before archiving" >&2; \
		exit 1; \
	}
	# Keep proxy artifacts only; Go extracts them into GOMODCACHE during build.
	find dist/go-mod -mindepth 1 -maxdepth 1 ! -name cache -exec rm -rf {} +
	XZ_OPT='-T1 -9' tar --sort=name --mtime="@$(SOURCE_DATE_EPOCH)" \
		--owner=0 --group=0 --numeric-owner \
		-C dist -cJf "dist/arise-$(VERSION)-deps.tar.xz" go-mod
	rm -rf dist/go-mod
	rm -f dist/.go-module-input.sha256
	@echo "Created dist/arise-$(VERSION)-deps.tar.xz"
