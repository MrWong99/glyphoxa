# Glyphoxa Makefile
# Requires: Go 1.26+, CGO_ENABLED=1

.PHONY: build test lint vet fmt check clean

# Build
build:
	go build -o bin/glyphoxa ./cmd/glyphoxa

# Run all tests with race detector
test:
	go test -race -count=1 ./...

# Run tests with verbose output
test-v:
	go test -race -count=1 -v ./...

# Run tests with coverage
test-cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	@echo "HTML report: go tool cover -html=coverage.out"

# Lint with golangci-lint (install: https://golangci-lint.run/welcome/install/)
lint:
	golangci-lint run ./...

# Go vet
vet:
	go vet ./...

# Format check
fmt:
	gofmt -l -w .

# Full pre-commit check
check: fmt vet test
	@echo "All checks passed ✓"

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out
