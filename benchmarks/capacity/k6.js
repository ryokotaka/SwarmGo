import http from 'k6/http';

export const options = {
  discardResponseBodies: true,
  scenarios: { requests: {
    executor: 'shared-iterations', vus: Number(__ENV.CONCURRENCY),
    iterations: Number(__ENV.REQUESTS), maxDuration: '5m', gracefulStop: '0s',
  }},
  thresholds: {http_req_failed: ['rate==0']},
};
const body = open('/bench/body.json');
export default function () {
  http.request(__ENV.METHOD, __ENV.TARGET, __ENV.METHOD === 'POST' ? body : null,
    {timeout: '30s', headers: {'Content-Type': 'application/json', 'Accept-Encoding': 'identity'}});
}
export function handleSummary(data) {
  return { [__ENV.SUMMARY]: JSON.stringify(data, null, 2) };
}
