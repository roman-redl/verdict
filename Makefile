BINARY := bin/verdict

# The build version shown in the first log line of both binaries.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test lint fmt vet tidy vuln check run web ask clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/verdict
	go build -ldflags "$(LDFLAGS)" -o bin/verdictd ./cmd/verdictd

# Tests run with the race detector: the server's worker runs next to the HTTP handlers.
test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

tidy:
	go mod tidy

# Known vulnerabilities in the code that is actually reachable (govulncheck is a go.mod tool).
vuln:
	go tool govulncheck ./...

# Full check before a commit: build + static analysis + tests + linter + vulnerabilities.
check: build vet test lint vuln

# Run a full assessment: make run QUESTION="does creatine improve cognition"
run: build
	@test -n "$(QUESTION)" || (echo "usage: make run QUESTION=\"...\""; exit 1)
	$(BINARY) run -q "$(QUESTION)"

# Discovery mode C, step 1: open Consensus with the question ready: make web QUESTION="..."
web: build
	@test -n "$(QUESTION)" || (echo "usage: make web QUESTION=\"...\""; exit 1)
	$(BINARY) web -q "$(QUESTION)"

# Q&A over a finished run: make ask RUN_DIR=runs/<...>
ask: build
	@test -n "$(RUN_DIR)" || (echo "usage: make ask RUN_DIR=runs/<...>"; exit 1)
	$(BINARY) ask --run-dir $(RUN_DIR)

clean:
	rm -rf bin
