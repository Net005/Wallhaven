# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/wallhaven-control .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && mkdir -p /data /wallpapers && chmod 777 /data /wallpapers
COPY --from=build /out/wallhaven-control /usr/local/bin/wallhaven-control

LABEL org.opencontainers.image.source="https://github.com/Net005/Wallhaven" \
      org.opencontainers.image.title="Wallhaven Control" \
      org.opencontainers.image.description="Web control panel and live monitor for the Wallhaven downloader"

ENV WH_DATA=/data \
    WH_BASE_DIR=/wallpapers \
    WH_LISTEN=:8080 \
    TZ=Europe/Amsterdam
VOLUME ["/data", "/wallpapers"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["wallhaven-control"]
