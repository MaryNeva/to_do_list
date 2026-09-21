# syntax=docker/dockerfile:1

# Track the Go minor line (not a patch) so rebuilds get stdlib security fixes.
# Must match GO_VERSION in CI; deployments/toolchain_test.go checks this.
FROM golang:1.25-alpine AS build
WORKDIR /src

# Separate layer so source edits do not re-download modules. No go mod tidy:
# the image must build exactly what go.mod/go.sum pin.
COPY go.mod go.sum ./
RUN go mod download

# Copy only build inputs so unrelated files do not invalidate the cache.
COPY cmd ./cmd
COPY internal ./internal

# Exposed via todo_build_info and GET /healthz.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# -mod=readonly fails instead of modifying go.mod/go.sum.
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
# Required at startup: there are no compiled-in defaults. Secrets come from the environment.
COPY config.yaml ./config.yaml

USER app
EXPOSE 8080

ENTRYPOINT ["./server"]
