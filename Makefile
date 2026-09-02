# Fuzz targets run one at a time (go test -fuzz takes a single package, so each
# runs against the root package "." where the FuzzXxx tests live). List every
# FuzzXxx here as it is added.
FUZZ_TARGETS := FuzzParse

# Where `rotini-build` writes the dogfood binary. Honors GOBIN, falling back to
# the default $(go env GOPATH)/bin, so the target is not tied to one machine.
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: all clean lint test test-acceptance test-bench test-fuzz test-mutation test-race rotini rotini-build rotini-install

all: clean lint test test-acceptance test-bench test-fuzz test-mutation test-race rotini-build rotini-install

clean:
	@rm -rf *.out test_mutation.json

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

test-bench:
	@go test -bench=. -benchmem -count=1 ./... | tee test_bench.out

test-fuzz:
	@for target in $(FUZZ_TARGETS); do \
		echo "→ $$target"; \
		go test -fuzz="^$$target$$" -fuzztime=60s -run=^$$ . ; \
	done

test-mutation:
	@go tool github.com/go-gremlins/gremlins/cmd/gremlins unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out

# rotini is the verification-chain entry point (see .docs/ROTINI_WORK_ITEMS.md).
# Same order as `all`: build the versioned binary, then regenerate and install.
rotini: rotini-build rotini-install

rotini-build:
	@go build -ldflags "-s -w -X main.version=1.2.3" -o $(GOBIN)/rotini ./cmd/rotini/main.go

rotini-install:
	@go generate ./...
	@go install ./cmd/...
