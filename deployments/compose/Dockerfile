# Multi-stage build for Glyphoxa
FROM golang:1.26 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o glyphoxa ./cmd/glyphoxa

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /build/glyphoxa /usr/local/bin/glyphoxa
ENTRYPOINT ["glyphoxa"]
