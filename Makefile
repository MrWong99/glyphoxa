# Glyphoxa Makefile
# Requires: Go 1.26+, CGO_ENABLED=1

.PHONY: build test lint vet fmt check clean whisper-libs

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

# Build whisper.cpp static library for local CGO compilation.
# After running this, set the environment before other targets:
#   export C_INCLUDE_PATH=/tmp/whisper-install/include
#   export LIBRARY_PATH=/tmp/whisper-install/lib
#   export CGO_ENABLED=1
WHISPER_SRC  := /tmp/whisper-src
WHISPER_DEST := /tmp/whisper-install

whisper-libs:
	@if [ -f "$(WHISPER_DEST)/include/whisper.h" ]; then \
		echo "whisper.cpp already built at $(WHISPER_DEST)"; \
	else \
		echo "Cloning whisper.cpp..."; \
		git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git $(WHISPER_SRC); \
		echo "Building whisper.cpp..."; \
		cmake -B $(WHISPER_SRC)/build -S $(WHISPER_SRC) \
			-DCMAKE_BUILD_TYPE=Release \
			-DBUILD_SHARED_LIBS=OFF \
			-DGGML_NATIVE=ON \
			-DWHISPER_BUILD_TESTS=OFF \
			-DWHISPER_BUILD_SERVER=OFF; \
		cmake --build $(WHISPER_SRC)/build --config Release -j$$(nproc); \
		mkdir -p $(WHISPER_DEST)/include $(WHISPER_DEST)/lib; \
		cp $(WHISPER_SRC)/include/whisper.h $(WHISPER_DEST)/include/; \
		cp -r $(WHISPER_SRC)/ggml/include/* $(WHISPER_DEST)/include/ 2>/dev/null || true; \
		find $(WHISPER_SRC)/build -name '*.a' -exec cp {} $(WHISPER_DEST)/lib/ \;; \
		echo "whisper.cpp installed to $(WHISPER_DEST)"; \
	fi
	@echo ""
	@echo "Run the following to enable whisper CGO builds:"
	@echo "  export C_INCLUDE_PATH=$(WHISPER_DEST)/include"
	@echo "  export LIBRARY_PATH=$(WHISPER_DEST)/lib"
	@echo "  export CGO_ENABLED=1"

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out
