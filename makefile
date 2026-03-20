BINARY_NAME=nosygit

.PHONY: help build run run-start test fmt tidy clean

help:
	@echo "Targets:"
	@echo "  build      Build the nosygit binary"
	@echo "  run        Build and show CLI help"
	@echo "  run-start  Build and run daemon start (uses ./state.db)"
	@echo "  test       Run all tests"
	@echo "  fmt        Format Go source"
	@echo "  tidy       Tidy Go modules"
	@echo "  clean      Remove build output"

build:
	go build -o bin/${BINARY_NAME} main.go

run: build
	./bin/${BINARY_NAME} --help

run-start: build
	./bin/${BINARY_NAME} start

test:
	go test ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	go clean
	rm -f bin/${BINARY_NAME}
