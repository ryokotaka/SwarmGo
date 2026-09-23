wrk.method = "POST"
wrk.body = '{"data":"' .. string.rep("x", 1013) .. '"}'
wrk.headers["Content-Type"] = "application/json"
wrk.headers["Accept-Encoding"] = "identity"
done = function(summary, latency, requests)
  local f = assert(io.open(os.getenv("SUMMARY"), "w"))
  f:write(string.format('{"requests":%d,"duration_usec":%d,"errors":{"connect":%d,"read":%d,"write":%d,"status":%d,"timeout":%d}}\n',
    summary.requests, summary.duration, summary.errors.connect, summary.errors.read,
    summary.errors.write, summary.errors.status, summary.errors.timeout))
  f:close()
end
