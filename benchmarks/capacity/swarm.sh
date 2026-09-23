#!/bin/sh
set -eu
/bench/bin/${SWARMGO_BINARY:-swarmgo} run -workers 1 -p 50051 -url "$TARGET" -n "$REQUESTS" -c "$CONCURRENCY" -method "$METHOD" -header 'Content-Type: application/json' -header 'Accept-Encoding: identity' -body-file "$BODY_FILE" -timeout 5m -output "$SUMMARY" >"$SUMMARY.master.log" 2>&1 &
master=$!
# Start the worker only after the controller has opened its listener.
while ! grep -q '^Waiting for' "$SUMMARY.master.log"; do
  kill -0 "$master" 2>/dev/null || exit 1
  sleep 0.01
done
/bench/bin/${SWARMGO_BINARY:-swarmgo} worker -addr 127.0.0.1:50051 >"$SUMMARY.worker.log" 2>&1 &
worker=$!
trap 'kill "$master" "$worker" 2>/dev/null || true' EXIT INT TERM
wait "$master"
wait "$worker"
