.PHONY: lint test coverage integration build all clean

BINARY := bin/substrate-bootstrap
COVERAGE_FILE := coverage.out
COVERAGE_THRESHOLD := 80

lint:
	golangci-lint run

test:
	go test ./cmd/... ./internal/... -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic -timeout=60s

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

clean:
	rm -rf bin/ $(COVERAGE_FILE)
