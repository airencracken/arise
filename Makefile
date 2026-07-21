.PHONY: all build static test test-v test-worker test-shellcheck test-unit test-adversarial test-mutation test-race test-bench test-integration test-live-portage-compile test-coverage test-coverage-network test-coverage-benchmark vet lint clean install uninstall man info bench bench-quick bench-compare bench-json perf-harness perf-table perf-prepare perf-smoke deps release

BINARY := arise
MODULE := github.com/airencracken/arise
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man
INFODIR ?= $(PREFIX)/share/info
DOCDIR ?= $(PREFIX)/share/doc/arise

GO ?= go
GOFLAGS ?= -trimpath -ldflags="-s -w"
CGO_ENABLED ?= 0
COVERAGE_CORE_PKGS := $(shell $(GO) list ./cmd/... ./internal/... | grep -Ev '/(benchmark|binpkg|fetch|integration)$$')

all: build test vet

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/arise/

static:
	CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -o $(BINARY) ./cmd/arise/
	@file $(BINARY) | grep -q "statically linked" || { file $(BINARY); echo "static build: FAILED" >&2; exit 1; }
	@echo "static build: OK"

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
	$(GO) test ./internal/benchmark/ -bench=. -benchtime=1s -count=1

bench-quick:
	$(GO) test ./internal/benchmark/ -bench=. -benchtime=100ms -count=1

bench-compare:
	$(GO) test -tags=live_portage ./internal/benchmark/ -run 'TestCompare' -v -count=1 -timeout 10m

bench-json:
	./$(BINARY) bench --json 2>/dev/null || $(GO) test ./internal/benchmark/ -bench=. -benchtime=1s

bench-all: bench bench-compare

perf-harness:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -o arise-perf ./cmd/arise-perf/
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -o arise-perf-table ./cmd/arise-perf-table/

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
	rm -f $(BINARY) arise-perf arise-perf-table
	rm -f /tmp/arise-coverage.out /tmp/arise-coverage.html
	$(GO) clean -testcache

BASHCOMPDIR ?= $(PREFIX)/share/bash-completion/completions

install: build
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

info:
	@echo "Info page: see arise.info (generate with: texi2any --info arise.texi)"

docs: man info
	@echo "Documentation built."

#
# Release
#
# See misc/RELEASE.md for the full release runbook.
#
# Quick summary:
#   1. Export VERSION, run make download, make test, make vet
#   2. make release VERSION=x.y.z  (tags and pushes)
#   3. cd ../arise-overlay && cp arise-9999.ebuild arise-x.y.z.ebuild
#   4. make manifest VERSION=x.y.z && git add . && git commit -m "release vx.y.z" && git push
#   5. On the Gentoo host: emerge --sync arise-overlay && emerge -av arise
#

VERSION ?= 0.1.1
release: download static test
	@echo "Tagging arise v$(VERSION)..."
	git tag -a "v$(VERSION)" -m "arise v$(VERSION)"
	git push origin master --tags
	@echo ""
	@echo "Tag pushed. Now update the overlay:"
	@echo ""
	@echo "  cd ../arise-overlay"
	@echo "  make manifest VERSION=$(VERSION)"
	@echo "  git add sys-apps/arise/Manifest sys-apps/arise/arise-$(VERSION).ebuild"
	@echo "  git commit -m 'release arise v$(VERSION)'"
	@echo "  git push origin master"
	@echo ""
	@echo "On the Gentoo host:"
	@echo "  emerge --sync arise-overlay"
	@echo "  emerge -av arise"

#
# Go module management. Release builds use a module-cache archive so the
# repository stays unvendored while Portage builds remain network-free.
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

vendor:
	@echo "Arise does not commit vendored dependencies; use 'make deps VERSION=x.y.z'."
	@exit 1

deps:
	@test -n "$(VERSION)" || { echo "VERSION is required"; exit 1; }
	rm -rf dist/go-mod
	mkdir -p dist/go-mod
	GOMODCACHE="$(CURDIR)/dist/go-mod" $(GO) mod download -modcacherw all
	GOMODCACHE="$(CURDIR)/dist/go-mod" GOPROXY=off $(GO) mod verify
	# Keep proxy artifacts only; Go extracts them into GOMODCACHE during build.
	find dist/go-mod -mindepth 1 -maxdepth 1 ! -name cache -exec rm -rf {} +
	tar -C dist -cJf "dist/arise-$(VERSION)-deps.tar.xz" go-mod
	rm -rf dist/go-mod
	@echo "Created dist/arise-$(VERSION)-deps.tar.xz"
