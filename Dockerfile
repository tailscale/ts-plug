# Multi-arch image for ts-plug. Cross-compiles Go on BUILDPLATFORM and
# ships an alpine runtime.
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     -t ghcr.io/tailscale/ts-plug:latest -f Dockerfile --push .
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# Installed on the builder so we can COPY the bundle to the runtime image
# without a RUN there — TARGETPLATFORM RUN needs QEMU registered on the host.
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags='-s -w' -o /out/ts-plug ./cmd/ts-multi-plug

FROM alpine:3.20
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ts-plug /usr/local/bin/ts-plug
ENTRYPOINT ["/usr/local/bin/ts-plug"]
