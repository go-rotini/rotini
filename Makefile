# Fuzz targets run one at a time (go test -fuzz takes a single package), so each is
# listed as "<package> <FuzzName>". List every FuzzXxx here as it is added.
#
#   FuzzParse          the argv grammar
#   FuzzSuggest        the suggestion ranking
#   FuzzSuggestionFacts reading a token and candidates from any error tree
#   FuzzValidateSpec   the spec loader: four codecs, schema, lint rules, source locator
#   FuzzValidateConf   the same for the conf
FUZZ_TARGETS := .:FuzzParse .:FuzzSuggest .:FuzzSuggestionFacts ./internal/codegen:FuzzValidateSpec ./internal/codegen:FuzzValidateConf

# The version `rotini-build` stamps into the dogfood binary: the checkout's own, from git.
# v1.2.0 on a tagged commit; v1.2.0-3-gabc1234 between tags, which rotini reads as the last
# release (1.2.0); a -dirty suffix with uncommitted changes. Outside a git checkout it falls
# back to 0.0.0, which the spec and conf version guard treats as a dev build and does not judge.
# A real version matters: a fixed stamp passes the guard for every document up to it, which
# hides exactly the version problems the guard exists to catch.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)

# Where `rotini-build` writes the dogfood binary. Honors GOBIN, falling back to
# the default $(go env GOPATH)/bin, so the target is not tied to one machine.
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

# The development tools (linters, the license and vulnerability checkers, the mutation tester and
# the JSON Schema code generator) are declared in tools.mod rather than go.mod, so a module that
# requires rotini never sees them in its own dependency graph. Run them through TOOL, and upgrade
# them with `make tools-upgrade`, never `go mod tidy -modfile=tools.mod`: tidy scans rotini's own
# packages and would copy the runtime's dependencies into the tools file.
TOOL := go tool -modfile=tools.mod

.PHONY: all check-generated clean lint test test-acceptance test-bench test-conformance test-e2e test-fuzz test-mutation test-race tools-upgrade vuln vuln-tools rotini rotini-build rotini-install

all: clean lint test test-conformance test-acceptance test-e2e test-bench test-fuzz test-mutation test-race rotini-build rotini-install

clean:
	@rm -rf *.out test_mutation.json

# Regenerates everything rotini commits from its own sources and fails if that changed a file:
# a source edited without regenerating would otherwise ship stale code with the tests green.
# It compares fingerprints taken before and after, so it answers the same with or without
# uncommitted work in the tree.
GENERATED_FINGERPRINT = find . -type f -not -path './.git/*' -not -path './docs/public/*' -not -name '*.out' | LC_ALL=C sort | xargs shasum

check-generated:
	@$(GENERATED_FINGERPRINT) > /tmp/rotini-generated.before
	@cp internal/codegen/schema-spec.json schema-spec.json
	@cp internal/codegen/schema-conf.json schema-conf.json
	@go generate ./cmd/rotini > /dev/null
	@go mod tidy
	@$(GENERATED_FINGERPRINT) > /tmp/rotini-generated.after
	@diff /tmp/rotini-generated.before /tmp/rotini-generated.after > /dev/null || \
		(echo "regenerating changed these files — commit the regenerated result:"; \
		 diff /tmp/rotini-generated.before /tmp/rotini-generated.after | grep '^>' | awk '{print "  " $$3}'; exit 1)

lint:
	@gofmt_unformatted=$$(gofmt -l . 2>/dev/null | grep -v '^testdata/' || true); \
	test -z "$$gofmt_unformatted" || (echo "files not formatted:" && echo "$$gofmt_unformatted" && exit 1)
	@go vet ./...
	@go mod verify
	@go mod verify -modfile=tools.mod
	@$(TOOL) golangci-lint run ./...
	@$(TOOL) go-licenses check ./...
	@$(MAKE) --no-print-directory vuln
	@$(MAKE) --no-print-directory vuln-tools

# govulncheck, one target per dependency graph; lint runs both.
#
# vuln scans go.mod from source, which reports only vulnerable code rotini can actually reach.
# -test brings the test files in, so a test-only module (go-internal) is checked too.
#
# vuln-tools scans tools.mod per tool, as a built binary: that is exactly the code that runs, and
# source mode cannot analyze another module's main packages. The tool list comes from tools.mod
# itself, so a newly added tool is scanned without editing this file.
vuln:
	@$(TOOL) govulncheck -test ./...

vuln-tools:
	@bin=$$(mktemp -d); trap 'rm -rf "$$bin"' EXIT; \
	for pkg in $$(go list -modfile=tools.mod tool); do \
		name=$$(echo "$$pkg" | tr / _); \
		echo "→ $$pkg"; \
		go build -modfile=tools.mod -o "$$bin/$$name" "$$pkg" || exit 1; \
		$(TOOL) govulncheck -mode=binary "$$bin/$$name" || exit 1; \
	done

# Upgrades every tool in tools.mod to its latest release. tools.mod is rebuilt from scratch
# rather than edited in place, so it ends up holding exactly what today's tools require: no
# indirect line left behind by a tool that stopped needing it, and no tidy. The module, go and
# toolchain lines are copied from go.mod, so both files agree on the Go version. On any failure
# the previous tools.mod and tools.sum are restored. On success the upgraded tools are
# vulnerability-scanned; review the diff and commit tools.mod and tools.sum together.
tools-upgrade:
	@tools=$$(go list -modfile=tools.mod tool) || exit 1; \
	backup=$$(mktemp -d); cp tools.mod tools.sum "$$backup"/; \
	restore() { cp "$$backup"/tools.mod "$$backup"/tools.sum .; rm -rf "$$backup"; echo "tools-upgrade failed; tools.mod and tools.sum restored"; exit 1; }; \
	{ grep -E '^module ' go.mod; echo; grep -E '^go ' go.mod; echo; grep -E '^toolchain ' go.mod; } > tools.mod; \
	rm -f tools.sum; \
	go get -modfile=tools.mod -tool $$(for t in $$tools; do printf '%s@latest ' "$$t"; done) || restore; \
	go mod verify -modfile=tools.mod || restore; \
	rm -rf "$$backup"
	@$(MAKE) --no-print-directory vuln-tools
	@echo "tools upgraded; review: git diff tools.mod"

test:
	@go test -v -count=1 -coverprofile=test.out ./...
	@go tool cover -func=test.out

test-acceptance:
	@go test -v -count=1 -run TestAcceptance -coverprofile=test_acceptance.out ./...
	@go tool cover -func=test_acceptance.out

# The in-process tier of the input conformance matrix (conformance_test.go). CI runs
# it on all three platforms; the process tier it splits with is `test-acceptance`.
test-conformance:
	@go test -v -count=1 -run TestConformance -coverprofile=test_conformance.out ./...
	@go tool cover -func=test_conformance.out

# The outside-in tier: testscript rigs under e2e/testdata/script that drive the real
# `rotini` binary through spec -> generate -> build -> RUN, the way a user does.
test-e2e:
	@go test -v -count=1 ./e2e/...

test-bench:
	@go test -bench=. -benchmem -count=1 ./... | tee test_bench.out

test-fuzz:
	@for target in $(FUZZ_TARGETS); do \
		pkg=$${target%%:*}; name=$${target#*:}; \
		echo "→ $$pkg $$name"; \
		go test -fuzz="^$$name$$" -fuzztime=60s -run=^$$ $$pkg || exit 1; \
	done

test-mutation:
	@$(TOOL) gremlins unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out

# rotini is the verification-chain entry point (see the tracker in the sibling .docs repo).
# Same order as `all`: build the versioned binary, then regenerate and install.
rotini: rotini-build rotini-install

rotini-build:
	@go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(GOBIN)/rotini ./cmd/rotini/main.go

rotini-install:
	@go generate ./...
	@go install ./cmd/...
