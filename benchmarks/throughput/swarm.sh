#!/bin/sh
set -u
# WORKERS splits CONCURRENCY evenly across that many worker processes, each
# with the environment's GOMAXPROCS (for example one Go processor per worker).
workers=${WORKERS:-1}
per_worker=$((CONCURRENCY / workers))
if [ $((per_worker * workers)) -ne "$CONCURRENCY" ]; then
  echo "CONCURRENCY must be divisible by WORKERS" >&2
  exit 2
fi
/bench/bin/swarmgo run -workers "$workers" -p 50051 -url "$TARGET" -n 2147483647 -c "$per_worker" -method POST -header 'Content-Type: application/json' -header 'Accept-Encoding: identity' -body-file /bench/body.json -timeout "${DURATION}s" -output /results/native.json >/results/master.log 2>&1 &
master=$!
pids=''
trap 'kill "$master" $pids 2>/dev/null || true' EXIT INT TERM
while ! grep -q '^Waiting for' /results/master.log; do
  kill -0 "$master" 2>/dev/null || exit 1
  sleep .01
done
i=1
while [ "$i" -le "$workers" ]; do
  log=/results/worker.log
  [ "$workers" -gt 1 ] && log=/results/worker-$i.log
  /bench/bin/swarmgo worker -addr 127.0.0.1:50051 >"$log" 2>&1 &
  pids="$pids $!"
  i=$((i + 1))
done
wait "$master"
code=$?
for pid in $pids; do
  wait "$pid" || true
done
exit "$code"
