# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine3.24 AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -mod=readonly -buildvcs=false -trimpath \
    -ldflags="-s -w" -o /out/tg-client ./cmd/tg-client

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /data

WORKDIR /

COPY --from=build /out/tg-client /usr/local/bin/tg-client

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/tg-client"]
CMD ["-config", "/config.yaml"]
