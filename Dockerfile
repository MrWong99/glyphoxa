FROM golang:1.26 AS build

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o /out/glyphoxa -ldflags='-s -w -linkmode external -extldflags "-static"' ./cmd/glyphoxa

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/glyphoxa /usr/local/bin/glyphoxa

ENTRYPOINT ["glyphoxa"]
