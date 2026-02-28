# =============================================================================
# Compose-specific Dockerfile with native whisper.cpp bindings
# =============================================================================
#
# Same multi-stage build as the root Dockerfile. Whisper.cpp is compiled from
# source and statically linked — no separate whisper container needed.
#
# Mount model files at runtime:
#   volumes:
#     - ./models:/models:ro
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Build whisper.cpp static library
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS whisper-build

RUN apt-get update && apt-get install -y --no-install-recommends \
    cmake g++ make git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG WHISPER_CPP_VERSION=master

RUN git clone --depth 1 --branch ${WHISPER_CPP_VERSION} \
    https://github.com/ggml-org/whisper.cpp.git /whisper.cpp

WORKDIR /whisper.cpp

RUN cmake -B build \
    -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF \
    -DWHISPER_BUILD_EXAMPLES=OFF \
    -DWHISPER_BUILD_TESTS=OFF \
    -DWHISPER_BUILD_SERVER=OFF \
    && cmake --build build --config Release -j$(nproc)

# ---------------------------------------------------------------------------
# Stage 2: Build Glyphoxa Go binary
# ---------------------------------------------------------------------------
FROM golang:1.26 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY --from=whisper-build /whisper.cpp/include /whisper.cpp/include
COPY --from=whisper-build /whisper.cpp/ggml/include /whisper.cpp/ggml/include
COPY --from=whisper-build /whisper.cpp/build/src/libwhisper.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml-base.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml-cpu.a /whisper.cpp/lib/

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV C_INCLUDE_PATH=/whisper.cpp/include:/whisper.cpp/ggml/include
ENV LIBRARY_PATH=/whisper.cpp/lib

RUN CGO_ENABLED=1 go build \
    -o glyphoxa \
    -ldflags='-s -w -linkmode external -extldflags "-static"' \
    ./cmd/glyphoxa

# ---------------------------------------------------------------------------
# Stage 3: Final minimal image
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/glyphoxa /usr/local/bin/glyphoxa

ENTRYPOINT ["glyphoxa"]
