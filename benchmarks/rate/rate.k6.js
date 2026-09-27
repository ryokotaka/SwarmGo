// Constant arrival rate for the rate ladder. Load only: every request is a
// validated POST, so the target's counter measures delivered load directly.
import http from 'k6/http';

const body = open('/bench/body.json');
const concurrency = Number(__ENV.CONCURRENCY);

export const options = {
  discardResponseBodies: true,
  scenarios: {
    load: {
      executor: 'constant-arrival-rate', rate: Number(__ENV.RATE), timeUnit: '1s',
      duration: __ENV.SECONDS + 's', preAllocatedVUs: concurrency, maxVUs: concurrency,
      gracefulStop: '5s',
    },
  },
};

export default function () {
  http.post(__ENV.TARGET, body, {
    timeout: '5s',
    headers: { 'Content-Type': 'application/json', 'Accept-Encoding': 'identity' },
  });
}

export function handleSummary(data) {
  return { [__ENV.SUMMARY]: JSON.stringify(data, null, 2) };
}
