VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION) -X main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Wails links against webkit2gtk. Distros from Ubuntu 24.04 / Debian 13 on
# ship only webkit2gtk-4.1 — pkg-config for the 4.0 default then fails and
# the GUI build dies at cgo. The webkit2_41 tag switches Wails to 4.1; probe
# for 4.0 rather than hardcoding so older distros still build unchanged.
# Mirrors the -tags webkit2_41 in .github/workflows/release.yml (build-linux).
ifeq ($(shell uname -s),Linux)
WAILS_TAGS := $(shell pkg-config --exists webkit2gtk-4.0 2>/dev/null || echo -tags webkit2_41)
endif

# Both the CLI and the Wails GUI share internal/ packages — build them together.
.PHONY: build
build: build-cli build-app

.PHONY: build-cli
build-cli:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli ./cmd/monoagentcli

.PHONY: build-app
build-app:
	cd wails-app && wails build -ldflags "$(LDFLAGS)" $(WAILS_TAGS)
	mkdir -p bin
	cp wails-app/build/bin/monoagent-ui bin/MonoAgent

.PHONY: dev
dev:
	cd wails-app && wails dev

.PHONY: build-all
build-all: build-cli
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli-darwin-amd64 ./cmd/monoagentcli
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli-darwin-arm64 ./cmd/monoagentcli
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli-linux-amd64 ./cmd/monoagentcli
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli-linux-arm64 ./cmd/monoagentcli
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoagentcli-windows-amd64.exe ./cmd/monoagentcli

.PHONY: test
test:
	go test -race -v ./...

.PHONY: lint
lint:
	golangci-lint run ./...

# Benchmark suite for engine hot paths (expression evaluation, per-node item
# throughput, workflow store save/load, secret redaction). Scoped to just
# the packages that hold benchmarks, not the whole repo, to keep it fast.
# -count=5 + benchstat gives a stable-enough signal for regression tracking;
# see .github/workflows/bench.yml for the scheduled CI run.
.PHONY: bench
bench:
	go test -bench=. -benchtime=3s -count=5 -run=^$$ \
		./internal/workflow/... ./internal/nodes/control/... \
		> bench.txt

.PHONY: clean
clean:
	rm -rf bin/

.PHONY: tidy
tidy:
	go mod tidy
	cd wails-app && go mod tidy

# Manual release: bump version, tag, push. Usage: make release [v=minor|major]
# Every push to master auto-creates a patch release via GitHub Actions.
# Use this for intentional minor/major bumps:
#   make release          → v0.1.5 → v0.2.0 (minor)
#   make release v=major  → v0.2.0 → v1.0.0
.PHONY: release
release:
	@LAST=$$(git tag --sort=-v:refname | head -1); \
	if [ -z "$$LAST" ]; then \
		NEXT="v0.1.0"; \
	else \
		MAJOR=$$(echo $$LAST | sed 's/v//' | cut -d. -f1); \
		MINOR=$$(echo $$LAST | sed 's/v//' | cut -d. -f2); \
		PATCH=$$(echo $$LAST | sed 's/v//' | cut -d. -f3); \
		case "$(v)" in \
			major) MAJOR=$$((MAJOR+1)); MINOR=0; PATCH=0;; \
			minor) MINOR=$$((MINOR+1)); PATCH=0;; \
			*)     MINOR=$$((MINOR+1)); PATCH=0;; \
		esac; \
		NEXT="v$$MAJOR.$$MINOR.$$PATCH"; \
	fi; \
	echo "Tagging $$NEXT ..."; \
	git tag -a "$$NEXT" -m "Release $$NEXT"; \
	git push origin master --tags; \
	echo "✓ Pushed $$NEXT — GitHub Actions will create the release"
