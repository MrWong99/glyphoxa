# =============================================================================
# Multi-stage build for Glyphoxa with native whisper.cpp bindings
# =============================================================================
#
# The whisper.cpp library is compiled from source and statically linked into
# the Go binary via CGO. This eliminates the need for a separate whisper
# container and removes all HTTP overhead from speech-to-text inference.
#
# The final image is based on distroless and contains only the static binary.
# Whisper model files are NOT bundled — mount them at runtime via a volume.
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
    -DGGML_NATIVE=OFF \
    && cmake --build build --config Release -j$(nproc)

# ---------------------------------------------------------------------------
# Stage 2: Build Glyphoxa Go binary
# ---------------------------------------------------------------------------
FROM golang:1.26 AS build

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ libc6-dev libopus-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy whisper.cpp headers and static libraries from the build stage.
COPY --from=whisper-build /whisper.cpp/include /whisper.cpp/include
COPY --from=whisper-build /whisper.cpp/ggml/include /whisper.cpp/ggml/include
COPY --from=whisper-build /whisper.cpp/build/src/libwhisper.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml-base.a /whisper.cpp/lib/
COPY --from=whisper-build /whisper.cpp/build/ggml/src/libggml-cpu.a /whisper.cpp/lib/

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV C_INCLUDE_PATH=/whisper.cpp/include:/whisper.cpp/ggml/include
ENV LIBRARY_PATH=/whisper.cpp/lib

RUN CGO_ENABLED=1 go build \
    -o /out/glyphoxa \
    -ldflags='-s -w -linkmode external -extldflags "-static"' \
    ./cmd/glyphoxa

# ---------------------------------------------------------------------------
# Stage 3: Final minimal image
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/glyphoxa /usr/local/bin/glyphoxa

ENTRYPOINT ["glyphoxa"]
