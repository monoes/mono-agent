VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION) -X main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: build
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoes ./cmd/monoes

.PHONY: build-all
build-all:
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoes-darwin-amd64 ./cmd/monoes
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoes-darwin-arm64 ./cmd/monoes
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoes-linux-amd64 ./cmd/monoes
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/monoes-windows-amd64.exe ./cmd/monoes

.PHONY: test
test:
	go test -race -v ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: clean
clean:
	rm -rf bin/

.PHONY: tidy
tidy:
	go mod tidy

# Release: bump version, tag, push. Usage: make release [v=patch|minor|major]
# Defaults to patch. Examples:
#   make release          → v0.1.0 → v0.1.1
#   make release v=minor  → v0.1.1 → v0.2.0
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
			*)     PATCH=$$((PATCH+1));; \
		esac; \
		NEXT="v$$MAJOR.$$MINOR.$$PATCH"; \
	fi; \
	echo "Tagging $$NEXT ..."; \
	git tag -a "$$NEXT" -m "Release $$NEXT"; \
	git push origin master --tags; \
	echo "✓ Pushed $$NEXT — GitHub Actions will create the release"
