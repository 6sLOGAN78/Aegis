import http from 'k6/http';
import { check } from 'k6';

// Baseline direct backend benchmark (without Aegis Zero-Trust Gateway).
// Generates a sustained 1,000 requests/sec load for 30 seconds against the backend mock.
export const options = {
  scenarios: {
    constant_rate: {
      executor: 'constant-arrival-rate',
      rate: 1000, // 1,000 RPS
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    'http_req_duration{status:200}': ['p(95)<5', 'p(99)<10'], // Baseline raw backend latency
    'http_req_failed': ['rate<0.01'], // 99% success rate
  },
};

export default function () {
  const targetUrl = __ENV.TARGET_URL || 'http://localhost:8081/orders';
  const res = http.get(targetUrl);
  check(res, {
    'status is 200': (r) => r.status === 200,
  });
}
