#!/usr/bin/env sh
set -eu
BASE=${BASE:-http://localhost:8081}
echo "Writing before failure..."
curl -fsS -X PUT "$BASE/kv/failure-demo" -H 'Content-Type: application/json' -d '{"value":"survives"}'
echo
echo "Stop one Docker Compose node in another terminal, e.g.: docker compose stop node2"
printf 'Press Enter after the node has stopped: '
read -r confirmation
echo "Then verify the value remains readable from a surviving node:"
curl -fsS http://localhost:8083/kv/failure-demo
echo
