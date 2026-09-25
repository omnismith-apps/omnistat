BINARY   := omnistat
MODULE   := github.com/omnismith-apps/omnistat
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GORELEASER := go run github.com/goreleaser/goreleaser/v2@v2.17.1

.PHONY: all build crosscheck release-check release-snapshot run test test-race lint vet fmt tidy sandbox specs-check clean help

# Every target constitution V requires a static binary for, plus the Windows pairs
# that 004 NFR-005 keeps compiling so the cross-platform readings cannot rot.
CROSS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

all: fmt vet lint test build ## Full local gate (same as CI)

build: ## Build ./bin/omnistat
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/$(BINARY)

crosscheck: ## Verify the binary builds CGO-free for every target (004 NFR-005)
	@set -e; for pair in $(CROSS); do \
	  goos=$${pair%%/*}; goarch=$${pair##*/}; \
	  printf '  %-16s' "$$pair"; \
	  CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath -o /dev/null ./cmd/$(BINARY); \
	  echo ok; \
	done

release-check: ## Validate .goreleaser.yaml
	$(GORELEASER) check

release-snapshot: ## Build every release archive locally into dist/ (nothing is published)
	$(GORELEASER) release --snapshot --clean

run: ## Run from source: make run ARGS="schema plan" (sources ./.env if present)
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/$(BINARY) $(ARGS)

test: ## Unit tests
	go test ./...

test-race: ## Unit tests with the race detector and coverage
	go test -race -coverprofile=coverage.out -covermode=atomic ./...

lint: ## golangci-lint (install: https://golangci-lint.run/docs/welcome/install/)
	golangci-lint run ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt check (fails on unformatted files)
	@out=$$(gofmt -l . 2>/dev/null); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy: ## go mod tidy and verify
	go mod tidy
	go mod verify

sandbox: ## Run build-tagged tests against a real project (sources ./.env; creates entities, never deletes)
	@set -a; [ -f .env ] && . ./.env; set +a; go test -tags sandbox -run 'TestSandbox' -v ./... 2>&1 | grep -vE '^(=== RUN|\?|ok )'

specs-check: ## Validate spec folder structure and frontmatter
	./scripts/check-specs.sh

clean:
	rm -rf bin dist coverage.out

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'
