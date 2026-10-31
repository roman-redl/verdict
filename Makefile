BINARY := bin/verdict

.PHONY: build test lint fmt vet check run web ask clean

build:
	go build -o $(BINARY) ./cmd/verdict
	go build -o bin/verdictd ./cmd/verdictd

# Тесты с гонками: сервер крутит воркер параллельно с HTTP-хендлерами.
test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

# Полная проверка перед коммитом: сборка + статанализ + тесты + линтер.
check: build vet test lint

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
