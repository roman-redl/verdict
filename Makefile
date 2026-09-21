BINARY := bin/verdict

.PHONY: build test vet fmt check run web ask dois clean

build:
	go build -o $(BINARY) ./cmd/verdict
	go build -o bin/verdictd ./cmd/verdictd

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Full pre-commit gate: build + static analysis + tests + gofmt.
check: build vet test
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then \
		echo "gofmt: files not formatted:"; echo "$$out"; exit 1; \
	fi
	@echo "OK: build + vet + test + gofmt"

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
