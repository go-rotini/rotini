# Fuzz targets discovered automatically — every FuzzXxx test under ./... runs
# in test-fuzz when at least one matches. Empty until M1.8 lands the conformance
# suite + initial fuzz targets.
FUZZ_TARGETS :=

.PHONY: all clean lint test test-acceptance test-bench \
        test-conformance test-fuzz test-mutation test-race

all: clean lint test test-acceptance test-bench test-conformance test-fuzz test-mutation test-race

clean:
	@rm -rf *.out test_mutation.json

lint:
	@gofmt_unformatted=$$(gofmt -l . 2>/dev/null | grep -v '^testdata/' || true); \
	test -z "$$gofmt_unformatted" || (echo "files not formatted:" && echo "$$gofmt_unformatted" && exit 1)
	go vet ./...
	go mod verify
	go tool golangci-lint run ./...
	go tool go-licenses check ./...
	go tool govulncheck ./...

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
		go test -fuzz="^$$target$$" -fuzztime=60s -run=^$$ ./... ; \
	done

test-mutation:
	@go tool github.com/go-gremlins/gremlins/cmd/gremlins unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out
