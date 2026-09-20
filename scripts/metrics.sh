#!/usr/bin/env bash

set -uo pipefail

compose() { docker compose --env-file /dev/null "$@"; }

# printenv inside the container is the only source that cannot disagree with
# what the service actually read at startup.
container_env() {
	compose exec -T app printenv "$1" 2>/dev/null | tr -d '\r'
}

address="$(container_env METRICS_ADDRESS)"
body=""

if [[ -n "$address" ]]; then
	path="$(container_env METRICS_PATH)"
	[[ -n "$path" ]] || path="/metrics"
	namespace="$(container_env METRICS_NAMESPACE)"
	[[ -n "$namespace" ]] || namespace="todo"

	body="$(compose exec -T app wget -qO- "http://127.0.0.1:${address##*:}${path}" 2>/dev/null)"
	source="the app container on ${address}"
else
	path="${METRICS_PATH:-/metrics}"
	namespace="${METRICS_NAMESPACE:-todo}"
	port="${SERVER_PORT:-8080}"

	# A local run may also have been given a listener of its own.
	if [[ -n "${METRICS_ADDRESS:-}" ]]; then
		port="${METRICS_ADDRESS##*:}"
	fi

	body="$(curl -fsS "http://localhost:${port}${path}" 2>/dev/null)"
	source="localhost:${port}${path}"
fi

if [[ -z "$body" ]]; then
	echo "no metrics from ${source}" >&2
	echo "is the service running? (make run, or make docker-up)" >&2
	exit 1
fi

# Histogram buckets are dropped: they are many and they are not what anyone
# reads at a prompt.
printf '%s\n' "$body" | grep -E "^${namespace}_" | grep -v "_bucket{"
