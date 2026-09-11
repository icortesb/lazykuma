#!/usr/bin/env bash
# Starts a throwaway Uptime Kuma v2 for the integration test, with SQLite
# chosen so it is ready for the first user. `scripts/kuma-up.sh down` removes it.
set -euo pipefail

NAME=lazykuma-it
PORT=${LAZYKUMA_IT_PORT:-3902}
IMAGE=docker.io/louislam/uptime-kuma:2

if command -v podman >/dev/null; then
	RUN=podman
elif command -v docker >/dev/null; then
	RUN=docker
else
	echo "kuma-up: needs podman or docker" >&2
	exit 1
fi

"$RUN" rm -f "$NAME" >/dev/null 2>&1 || true
if [[ "${1:-}" == "down" ]]; then
	exit 0
fi

"$RUN" run -d --name "$NAME" -p "$PORT:3001" "$IMAGE" >/dev/null

url="http://localhost:$PORT"
for _ in $(seq 1 60); do
	if curl -fs "$url/setup-database-info" >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

# A new v2 asks which database to use before anything else.
if curl -fs "$url/setup-database-info" | grep -q '"needSetup":true'; then
	curl -fs -X POST -H 'Content-Type: application/json' \
		-d '{"dbConfig":{"type":"sqlite"}}' "$url/setup-database" >/dev/null
fi

# Kuma restarts into the app; wait until the page stops being the setup one.
for _ in $(seq 1 60); do
	if curl -fs "$url/api/entry-page" 2>/dev/null | grep -qv setup-database; then
		echo "kuma-up: $url"
		exit 0
	fi
	sleep 1
done
echo "kuma-up: Kuma did not come up at $url" >&2
exit 1
