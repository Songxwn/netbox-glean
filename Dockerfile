# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

# Download modules first so Docker can cache this layer.
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download || true

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy && \
    CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
      -o /out/netbox-glean ./cmd/netbox-glean

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 glean \
    && adduser -S -D -H -u 10001 -G glean glean \
    && mkdir -p /data \
    && chown glean:glean /data

COPY --from=build /out/netbox-glean /usr/local/bin/netbox-glean

USER glean
WORKDIR /data

ENV NETBOX_GLEAN_LISTEN=0.0.0.0:8080 \
    NETBOX_GLEAN_DATA_DIR=/data

VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/netbox-glean"]