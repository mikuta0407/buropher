# syntax=docker/dockerfile:1
# buropher container image.
#   docker build -t buropher --build-arg VERSION=$(git describe --tags --always) .
# Data (SQLite DB, attachments, generated secret key) lives in /data.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/buropher ./cmd/buropher

FROM alpine:3.22
# git: repository browsing (SCM), tzdata: server-local time for scheduled reminders, ca-certificates: SMTP/OIDC/Discord over TLS
RUN apk add --no-cache git ca-certificates tzdata \
 && addgroup -S -g 10001 buropher \
 && adduser -S -D -H -u 10001 -G buropher -h /data buropher \
 && mkdir -p /data \
 && chown buropher:buropher /data
COPY --from=build /out/buropher /usr/local/bin/buropher
ENV BUROPHER_ADDR=:3000 \
    BUROPHER_DB_DRIVER=sqlite \
    BUROPHER_DB_DSN=/data/buropher.db \
    BUROPHER_ATTACHMENTS_PATH=/data/files
USER buropher
WORKDIR /data
VOLUME ["/data"]
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/buropher"]
CMD ["serve"]
