.PHONY: all build static test test-v test-unit test-adversarial test-mutation test-race test-bench test-integration test-coverage vet lint clean install uninstall man info bench bench-quick bench-compare bench-json release

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

all: build test vet

build:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/arise/

static:
	CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -o $(BINARY) ./cmd/arise/
	@file $(BINARY) | grep -q "statically linked" && echo "static build: OK" || echo "static build: dynamic"

#
# Tests
#

test:
	$(GO) test ./internal/... -count=1 -timeout 120s

test-v:
	$(GO) test ./internal/... -v -count=1 -timeout 120s

test-unit:
	$(GO) test ./internal/... -run 'Test[^P]|TestP[a-ln-z]' -count=1 -timeout 60s

test-adversarial:
	$(GO) test ./internal/... -run 'Adversar|Mutation' -count=1 -timeout 60s

test-mutation:
	$(GO) test ./internal/... -run 'Mutation' -count=1 -timeout 60s

test-race:
	$(GO) test ./internal/... -race -count=1 -timeout 300s

test-integration:
	@if [ -d /var/db/repos/gentoo/metadata/md5-cache ]; then \
		echo "Running integration tests against live Gentoo tree..."; \
		$(GO) test ./internal/integration/ -run 'TestAllComparisons' -count=1 -v -timeout 300s; \
	else \
		echo "No Gentoo tree found. Skipping."; \
	fi

test-coverage:
	$(GO) test ./internal/... -coverprofile=/tmp/arise-coverage.out -covermode=atomic
	$(GO) tool cover -func=/tmp/arise-coverage.out

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
	$(GO) test ./internal/benchmark/ -run 'TestCompare' -v -count=1

bench-json:
	./$(BINARY) bench --json 2>/dev/null || $(GO) test ./internal/benchmark/ -bench=. -benchtime=1s

bench-all: bench bench-compare

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
	rm -f $(BINARY)
	rm -f /tmp/arise-coverage.out /tmp/arise-coverage.html
	$(GO) clean -testcache

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/
	install -d $(DESTDIR)$(MANDIR)/man1
	install -m 644 arise.1 $(DESTDIR)$(MANDIR)/man1/
	install -d $(DESTDIR)$(INFODIR)
	install -m 644 arise.info $(DESTDIR)$(INFODIR)/
	install -d $(DESTDIR)$(DOCDIR)
	install -m 644 README.md LICENSE $(DESTDIR)$(DOCDIR)/

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(MANDIR)/man1/arise.1
	rm -f $(DESTDIR)$(INFODIR)/arise.info

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

VERSION ?= 0.1.0
release: static test
	@echo "Tagging release v$(VERSION)..."
	git tag -a "v$(VERSION)" -m "arise v$(VERSION)"
	git push origin master --tags
	@echo ""
	@echo "Now update the overlay:"
	@echo "  cd ../arise-overlay && make manifest VERSION=$(VERSION)"
	@echo "  cd ../arise-overlay && git add . && git commit -m 'release v$(VERSION)' && git push"
