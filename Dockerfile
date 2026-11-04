# syntax=docker/dockerfile:1
# Server only. The web app ships as its own image (ghcr.io/julibilitv/jumedia-web) that installs
# its files into the /web volume, which this server serves; see deploy/compose.yaml.

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS server
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/jumedia ./cmd/jumedia

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata ffmpeg mesa-va-gallium \
 && if [ "$(apk --print-arch)" = "x86_64" ]; then apk add --no-cache intel-media-driver libva-intel-driver; fi \
 && adduser -D -H -u 10001 jumedia \
 && mkdir /data && chown jumedia /data
COPY --from=server /out/jumedia /usr/local/bin/jumedia
ENV JUMEDIA_ADDR=:8080 \
    JUMEDIA_DATA_DIR=/data \
    JUMEDIA_DB_DSN=/data/jumedia.db \
    JUMEDIA_WEB_DIR=/web \
    JUMEDIA_STATIC_DIR=/web \
    TZ=Asia/Shanghai
USER jumedia
WORKDIR /data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
  CMD wget -qO- http://127.0.0.1:8080/x/jumedia/site >/dev/null || exit 1
ENTRYPOINT ["jumedia"]
