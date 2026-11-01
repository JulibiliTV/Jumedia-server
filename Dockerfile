# syntax=docker/dockerfile:1
# Builds the server together with Jumedia-web, which is passed as the named build context `web`:
#   docker buildx build --build-context web=../Jumedia-web -t jumedia .

FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
WORKDIR /src
COPY --from=web package.json package-lock.json ./
RUN npm ci
COPY --from=web index.html tsconfig.json vite.config.ts ./
COPY --from=web public public
COPY --from=web src src
RUN VITE_MOCK=0 npm run build

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS server
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/jumedia ./cmd/jumedia

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata ffmpeg \
 && adduser -D -H -u 10001 jumedia \
 && mkdir /data && chown jumedia /data
COPY --from=server /out/jumedia /usr/local/bin/jumedia
COPY --from=webbuild /src/dist /app/web
COPY --from=webbuild /src/public /app/static
ENV JUMEDIA_ADDR=:8080 \
    JUMEDIA_DATA_DIR=/data \
    JUMEDIA_DB_DSN=/data/jumedia.db \
    JUMEDIA_WEB_DIR=/app/web \
    JUMEDIA_STATIC_DIR=/app/static \
    TZ=Asia/Shanghai
USER jumedia
WORKDIR /data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s \
  CMD wget -qO- http://127.0.0.1:8080/x/jumedia/site >/dev/null || exit 1
ENTRYPOINT ["jumedia"]
