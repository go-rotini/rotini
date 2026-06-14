# Fuzz targets run one at a time (go test -fuzz takes a single package, so each
# runs against the root package "." where the FuzzXxx tests live). List every
# FuzzXxx here as it is added.
FUZZ_TARGETS := FuzzParse

.PHONY: all clean lint test test-acceptance test-bench test-conformance test-fuzz test-mutation test-race rotini

all: clean lint test test-acceptance test-bench test-conformance test-fuzz test-mutation test-race rotini

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

test-conformance:
	@go test -v -count=1 -run TestConformance -coverprofile=test_conformance.out ./...
	@go tool cover -func=test_conformance.out

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

rotini:
	@go generate ./...
	@go install ./cmd/...
