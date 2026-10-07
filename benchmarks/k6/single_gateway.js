import http from 'k6/http';
import { check } from 'k6';

// Single Gateway proxy latency benchmark (DIST-01, Invariant 3).
// Measures proxy latency at 1,000 req/s sustained load, verifying p99 < 20ms added latency SLA.
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
    'http_req_duration{status:200}': ['p(95)<10', 'p(99)<20'], // <20ms p99 SLA
    'http_req_failed': ['rate<0.01'], // 99% success rate
  },
};

export default function () {
  const targetUrl = __ENV.TARGET_URL || 'http://localhost:8080/api/orders';
  const token = __ENV.TOKEN || '';

  const params = {
    headers: {
      'Authorization': 'Bearer ' + token,
      'X-Request-ID': 'k6-sg-' + __VU + '-' + __ITER,
    },
  };

  const res = http.get(targetUrl, params);
  check(res, {
    'status is 200': (r) => r.status === 200,
  });
}
