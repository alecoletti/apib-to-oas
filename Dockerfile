# syntax=docker/dockerfile:1.7
#
# apib-to-oas — single-binary CLI that converts API Blueprint to OpenAPI 3.x.

ARG GO_VERSION=1.26.2

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

WORKDIR /src

# Cache deps separately from sources for fast incremental builds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
      -o /out/apib-to-oas ./cmd/apib-to-oas

# ----------------------------------------------------------------------

FROM gcr.io/distroless/cc-debian12:nonroot

LABEL org.opencontainers.image.title="apib-to-oas"
LABEL org.opencontainers.image.description="Convert API Blueprint (incl. MSON) to OpenAPI 3.0/3.1/3.2."
LABEL org.opencontainers.image.source="https://github.com/alecoletti/apib-to-oas"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/apib-to-oas /usr/local/bin/apib-to-oas

# Default to the user's mounted workdir.
WORKDIR /work
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/apib-to-oas"]
CMD ["--help"]

