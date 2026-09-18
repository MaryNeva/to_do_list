# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src

# go.sum* matches zero or one file, so this works whether or not go.sum has
# been generated yet. `go mod tidy` fills in/verifies go.sum against go.mod
# and the actual source, then the module cache is reused by `go build`
# below as long as go.mod/go.sum don't change on a later build.
COPY go.mod go.sum* ./
COPY . .
RUN go mod tidy && go mod download

# Stamped into the binary and exposed as todo_build_info and GET /healthz, so
# a running instance can say which revision it is.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
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
