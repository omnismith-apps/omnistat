BINARY   := omnistat
MODULE   := github.com/omnismith-apps/omnistat
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: all build run test test-race lint vet fmt tidy sandbox specs-check clean help

all: fmt vet lint test build ## Full local gate (same as CI)

build: ## Build ./bin/omnistat
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/$(BINARY)

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
