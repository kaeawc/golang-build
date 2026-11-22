# Developer entry points. `make help` lists targets.
#
# Dev tools (golangci-lint, gosec, govulncheck, ...) are pinned in
# tools/go.mod via `tool` directives and run with `go tool`, so there is
# nothing to install beyond Go itself. tools/ is a separate module to keep
# their dependencies out of the application's go.mod.

GO      ?= go
TOOL    := $(GO) tool -modfile=tools/go.mod
BIN_DIR ?= bin
CMDS    := $(notdir $(wildcard cmd/*))

# Reproducible builds: static binaries with no local paths baked in. Go
# stamps VCS info into every build; read it with debug.ReadBuildInfo.
# (CGO stays on for tests: the race detector needs it.)
BUILD   := CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w"

# Rationale for each exclusion lives in .golangci.yml.
GOSEC_EXCLUDES := G104,G304,G602,G703,G704

.DEFAULT_GOAL := help

##@ Build

.PHONY: build
build: ## Build every cmd/* binary into bin/
	$(BUILD) -o $(BIN_DIR)/ ./cmd/...

.PHONY: $(addprefix build-,$(CMDS))
$(addprefix build-,$(CMDS)): build-%: ## Build one binary, e.g. make build-server
	$(BUILD) -o $(BIN_DIR)/$* ./cmd/$*

.PHONY: run
run: ## Run the HTTP server
	$(GO) run ./cmd/server

.PHONY: docker
docker: ## Build the server container image
	docker build -f ci/Dockerfile -t golang-build:dev .

##@ Test

.PHONY: test
test: ## Run tests with the race detector and coverage
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: test-short
test-short: ## Run tests without the race detector (fast inner loop)
	$(GO) test -short ./...

.PHONY: test-junit
test-junit: ## Run tests as CI does, writing junit-report.xml
	$(TOOL) gotestsum --junitfile junit-report.xml --format standard-verbose -- \
		-race -count=1 -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: cover
cover: test ## Open the HTML coverage report
	$(GO) tool cover -html=coverage.out

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -run='^$$' -bench=. -benchmem ./...

FUZZTIME ?= 30s
.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME (default 30s) each
	@for pkg in $$($(GO) list ./...); do \
		for f in $$($(GO) test -list='^Fuzz' $$pkg | grep '^Fuzz'); do \
			echo "== $$pkg $$f"; \
			$(GO) test -run='^$$' -fuzz="^$$f$$" -fuzztime=$(FUZZTIME) $$pkg || exit 1; \
		done; \
	done

##@ Quality

.PHONY: fmt
fmt: ## Format code (gofumpt + goimports)
	$(TOOL) golangci-lint fmt ./...

.PHONY: lint
lint: ## Run golangci-lint
	$(TOOL) golangci-lint run ./...

.PHONY: lint-fix
lint-fix: ## Run golangci-lint and apply automatic fixes
	$(TOOL) golangci-lint run --fix ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: complexity
complexity: ## Flag functions with cyclomatic complexity over 10
	$(TOOL) gocyclo -over 10 -ignore '_test\.go$$|^web/' .

.PHONY: security
security: ## Run gosec
	$(TOOL) gosec -quiet -exclude=$(GOSEC_EXCLUDES) ./...

.PHONY: vuln
vuln: ## Check dependencies for known vulnerabilities
	$(TOOL) govulncheck ./...

.PHONY: licenses
licenses: ## Fail on forbidden, restricted or unknown dependency licenses
	$(TOOL) go-licenses check ./... --disallowed_types=forbidden,restricted,unknown

.PHONY: actionlint
actionlint: ## Lint GitHub Actions workflows
	$(TOOL) actionlint

.PHONY: validate-workflows
validate-workflows: actionlint ## Lint workflows and the scripts they call
	bash scripts/validate-workflows.sh

##@ Modules

.PHONY: tidy
tidy: ## Tidy go.mod in the app and tools modules
	$(GO) mod tidy
	cd tools && $(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	$(GO) mod tidy -diff
	cd tools && $(GO) mod tidy -diff

.PHONY: upgrade
upgrade: ## Upgrade app dependencies to their latest minor/patch versions
	$(GO) get -u -t ./...
	$(GO) mod tidy

.PHONY: upgrade-tools
upgrade-tools: ## Upgrade every pinned dev tool
	cd tools && $(GO) get -u tool && $(GO) mod tidy

##@ Release

.PHONY: release-check
release-check: ## Run the release readiness checklist
	bash scripts/release-check.sh

.PHONY: release-snapshot
release-snapshot: ## Build a local goreleaser snapshot into dist/ (needs goreleaser)
	goreleaser release --snapshot --clean

##@ Template

.PHONY: rename
# MODULE and NAME count only on the command line: shells often export NAME
# (WSL sets it to the hostname), which must not silently rename the project.
rename: ## Make the template yours: make rename MODULE=github.com/you/app [NAME=app]
	@test "$(origin MODULE)" = "command line" || { echo "usage: make rename MODULE=github.com/you/app [NAME=app]" >&2; exit 2; }
	$(GO) run ./scripts/rename-module -module '$(MODULE)' $(if $(filter command line,$(origin NAME)),-name '$(NAME)')

.PHONY: rename-test
rename-test: ## Rename a scratch copy to a throwaway module, then lint, test and build it
	bash scripts/rename-test.sh

##@ Aggregates

.PHONY: check
check: tidy-check vet lint test ## Fast pre-push gate

.PHONY: ci
ci: tidy-check vet lint complexity security vuln licenses actionlint test rename-test ## Everything CI runs (except CodeQL and the image build)

.PHONY: clean
clean: ## Remove build and test artifacts
	rm -rf $(BIN_DIR) dist coverage.out junit-report.xml gosec-report.xml

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_%-]+:.*##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
