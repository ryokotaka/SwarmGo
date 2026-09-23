import http from 'k6/http';
export const options = {
  discardResponseBodies: true,
  scenarios: { load: { executor: 'constant-vus', vus: Number(__ENV.CONCURRENCY), duration: __ENV.DURATION + 's', gracefulStop: '5s' } },
  thresholds: { http_req_failed: ['rate==0'] },
};
const body = open('/bench/body.json');
export default function () {
  http.post(__ENV.TARGET, body, {timeout: '30s', headers: {'Content-Type': 'application/json', 'Accept-Encoding': 'identity'}});
}
export function handleSummary(data) { return {'/results/native.json': JSON.stringify(data, null, 2)}; }
