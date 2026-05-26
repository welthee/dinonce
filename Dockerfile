# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

# Layer-cached dependency download. Repeats are free until go.mod/sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags "-s -w \
            -X main.version=${VERSION} \
            -X main.commit=${COMMIT} \
            -X main.date=${DATE}" \
        -o /out/dinonce \
        ./cmd/dinonce

# Runtime: distroless static, non-root by default. No shell, no package
# manager, no libc — the binary is the only mutable surface area.
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="dinonce" \
      org.opencontainers.image.description="Distributed nonce ticketing service" \
      org.opencontainers.image.source="https://github.com/matelang/dinonce" \
      org.opencontainers.image.licenses="MIT"

# Migrations are read at startup; bake them into the image at a stable
# path. Config is mounted by the operator at /opt/dinonce/config.
COPY --from=builder /out/dinonce /opt/dinonce/dinonce
COPY scripts /opt/dinonce/scripts

WORKDIR /opt/dinonce
VOLUME /opt/dinonce/config
EXPOSE 5010 5001

ENTRYPOINT ["/opt/dinonce/dinonce"]
