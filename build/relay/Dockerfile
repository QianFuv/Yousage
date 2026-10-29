# syntax=docker/dockerfile:1
FROM node:24.15.0-bookworm-slim AS web
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@10.32.0 --activate
COPY tools/parity/package.json tools/parity/pnpm-lock.yaml ./tools/parity/
COPY frontend/package.json frontend/pnpm-lock.yaml ./frontend/
RUN pnpm --dir tools/parity install --frozen-lockfile \
    && pnpm --dir frontend install --frozen-lockfile
COPY tools/parity/ ./tools/parity/
COPY frontend/ ./frontend/
RUN pnpm --dir frontend build:web

FROM golang:1.26.10 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/yousage-relay/ ./cmd/yousage-relay/
COPY internal/relay/ ./internal/relay/
COPY --from=web /src/internal/relay/web/dist/ ./internal/relay/web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/yousage-relay ./cmd/yousage-relay \
    && mkdir -m 0700 /data

FROM gcr.io/distroless/static-debian12:nonroot
ENV YOUSAGE_RELAY_ADDR=:8080 \
    YOUSAGE_RELAY_DATA=/data
COPY --from=builder /out/yousage-relay /yousage-relay
COPY --from=builder --chown=65532:65532 /data/ /data/
USER 65532:65532
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/yousage-relay"]
