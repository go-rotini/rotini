# Fuzz targets run one at a time (go test -fuzz takes a single package), so each is
# listed as "<package> <FuzzName>". List every FuzzXxx here as it is added.
#
#   FuzzParse          the argv grammar
#   FuzzSuggest        the nine string-distance algorithms
#   FuzzValidateSpec   the spec loader: four codecs, schema, 35 lint rules, source locator
#   FuzzValidateConf   the same for the conf
FUZZ_TARGETS := .:FuzzParse .:FuzzSuggest ./internal/codegen:FuzzValidateSpec ./internal/codegen:FuzzValidateConf

# Where `rotini-build` writes the dogfood binary. Honors GOBIN, falling back to
# the default $(go env GOPATH)/bin, so the target is not tied to one machine.
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all check-generated clean lint test test-acceptance test-bench test-conformance test-e2e test-fuzz test-mutation test-race rotini rotini-build rotini-install

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
	@go tool golangci-lint run ./...
	@go tool go-licenses check ./...
	@go tool govulncheck ./...

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
	@go tool github.com/go-gremlins/gremlins/cmd/gremlins unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out

# rotini is the verification-chain entry point (see the tracker in the sibling .docs repo).
# Same order as `all`: build the versioned binary, then regenerate and install.
rotini: rotini-build rotini-install

rotini-build:
	@go build -ldflags "-s -w -X main.version=1.2.3" -o $(GOBIN)/rotini ./cmd/rotini/main.go

rotini-install:
	@go generate ./...
	@go install ./cmd/...
