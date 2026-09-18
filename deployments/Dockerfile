# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src

# Dependencies first, in their own layer: this step only re-runs when
# go.mod/go.sum change, so editing source code does not re-download the module
# graph. `go mod tidy` is deliberately NOT run here - the image must build
# exactly what the repository pins, and tidying inside the build would quietly
# repair a go.mod/go.sum that is broken for everyone else.
COPY go.mod go.sum ./
RUN go mod download

# Only what the binary needs. Anything else that lands in the build context is
# excluded by .dockerignore, but listing the inputs explicitly keeps the cache
# from being invalidated by unrelated files.
COPY cmd ./cmd
COPY internal ./internal

# Stamped into the binary and exposed as todo_build_info and GET /healthz, so
# a running instance can say which revision it is.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# -mod=readonly fails the build if go.mod or go.sum would have to change,
# rather than changing them.
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath \
    -ldflags="-s -w \
      -X to-do-list/internal/buildinfo.version=${VERSION} \
      -X to-do-list/internal/buildinfo.commit=${COMMIT} \
      -X to-do-list/internal/buildinfo.date=${BUILD_DATE}" \
    -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=build /out/server ./server
COPY migrations ./migrations
# config.yaml holds every non-secret setting and has no compiled-in
# fallbacks, so the image would not start without it. Secrets still come
# from the environment (docker-compose.yml passes them in).
COPY config.yaml ./config.yaml

USER app
EXPOSE 8080

ENTRYPOINT ["./server"]
