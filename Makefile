.PHONY: lint test coverage integration build all clean security

BINARY := bin/substrate-bootstrap
COVERAGE_FILE := coverage.out
COVERAGE_THRESHOLD := 80

lint:
	golangci-lint run

test:
	go test ./cmd/... -race -timeout=60s
	go test ./internal/... -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic -timeout=60s

coverage: test
	@total=$$(go tool cover -func=$(COVERAGE_FILE) | grep total | awk '{print $$3}' | tr -d '%'); \
	echo "Total coverage: $${total}%"; \
	if [ $$(echo "$${total} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage $${total}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi

integration:
	go test ./tests/supervisor/ -v -tags=integration -count=1 -timeout=180s

build:
	./scripts/build.sh

all: lint test coverage integration build

# Full module scan (including tests/e2e mock_node); keep helpers gosec-clean.
security:
	go run github.com/securego/gosec/v2/cmd/gosec@v2.25.0 -exclude-generated ./...

clean:
	rm -rf bin/ $(COVERAGE_FILE)
