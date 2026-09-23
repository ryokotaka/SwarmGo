import http from 'k6/http';
import exec from 'k6/execution';

const rate = Number(__ENV.RATE);
const duration = Number(__ENV.SECONDS);
const concurrency = Number(__ENV.CONCURRENCY);
const body = open('/bench/body.json');

export const options = {
  discardResponseBodies: true,
  scenarios: {
    load: {
      executor: 'constant-arrival-rate', exec: 'load', rate, timeUnit: '1s',
      duration: duration + 's', startTime: '1s', preAllocatedVUs: concurrency,
      maxVUs: concurrency, gracefulStop: '5s',
    },
    probe: {
      executor: 'constant-arrival-rate', exec: 'probe', rate: 10, timeUnit: '1s',
      duration: (duration + 2) + 's', preAllocatedVUs: 32, maxVUs: 32,
      gracefulStop: '5s',
    },
  },
  thresholds: {
    'http_reqs{scenario:load}': ['count>0'],
    'http_reqs{scenario:load,phase:measured}': ['count>0'],
    'http_req_failed{scenario:load}': ['rate==0'],
    'dropped_iterations{scenario:load}': ['count==0'],
    'http_reqs{scenario:probe}': ['count>0'],
    'http_req_failed{scenario:probe}': ['rate==0'],
    'dropped_iterations{scenario:probe}': ['count==0'],
  },
};

export function load() {
  const phase = Date.now() - exec.scenario.startTime >= Number(__ENV.WARMUP) * 1000
    ? 'measured' : 'warmup';
  http.post(__ENV.TARGET, body, {
    timeout: '5s', tags: { phase },
    headers: { 'Content-Type': 'application/json', 'Accept-Encoding': 'identity' },
  });
}

export function probe() {
  http.get(__ENV.TARGET, {
    timeout: '5s', headers: { 'Accept-Encoding': 'identity' },
  });
}

export function handleSummary(data) {
  return { [__ENV.SUMMARY]: JSON.stringify(data, null, 2) };
}
