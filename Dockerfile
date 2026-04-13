FROM golang:1.26-trixie AS build

WORKDIR /src

ARG TARGETOS=linux
ARG TARGETARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/steam-watcher ./cmd/server


FROM debian:trixie-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates tzdata libstdc++6 \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/steam-watcher /usr/local/bin/steam-watcher
COPY config.example.json /app/config.example.json

RUN useradd --system --create-home --home-dir /app --shell /usr/sbin/nologin appuser \
	&& mkdir -p /data \
	&& chown -R appuser:appuser /app /data

USER appuser

ENV CONFIG_PATH=/app/config.json
ENV DATABASE_PATH=/data/steam_status.duckdb
ENV APP_ADDR=:8080

EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/steam-watcher"]
