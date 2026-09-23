#!/bin/sh
set -u
/bench/bin/swarmgo run -workers 1 -p 50051 -url "$TARGET" -n 2147483647 -c "$CONCURRENCY" -method POST -header 'Content-Type: application/json' -header 'Accept-Encoding: identity' -body-file /bench/body.json -timeout "${DURATION}s" -output /results/native.json >/results/master.log 2>&1 &
master=$!
worker=''
trap 'kill "$master" ${worker:-} 2>/dev/null || true' EXIT INT TERM
while ! grep -q '^Waiting for' /results/master.log; do
  kill -0 "$master" 2>/dev/null || exit 1
  sleep .01
done
/bench/bin/swarmgo worker -addr 127.0.0.1:50051 >/results/worker.log 2>&1 &
worker=$!
wait "$master"
code=$?
wait "$worker" || true
exit "$code"
