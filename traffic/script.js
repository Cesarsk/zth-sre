import http from 'k6/http';
import { check } from 'k6';

const runID = __ENV.RUN_ID;
const scenario = __ENV.SCENARIO;
const profile = __ENV.PROFILE;
const targetURL = __ENV.TARGET_URL || 'http://api-lb:8080';

const profiles = {
  cpu: {
    executor: 'ramping-arrival-rate',
    startRate: 20,
    timeUnit: '1s',
    preAllocatedVUs: 20,
    maxVUs: 100,
    stages: [
      { target: 20, duration: '30s' },
      { target: 80, duration: '2m' },
      { target: 80, duration: '3m' },
    ],
    gracefulStop: '5s',
  },
  alert: {
    executor: 'constant-arrival-rate',
    rate: 10,
    timeUnit: '1s',
    duration: '5m',
    preAllocatedVUs: 10,
    maxVUs: 30,
    gracefulStop: '5s',
  },
  slo: {
    executor: 'constant-arrival-rate',
    rate: 20,
    timeUnit: '1s',
    duration: '5m',
    preAllocatedVUs: 20,
    maxVUs: 50,
    gracefulStop: '5s',
  },
};

if (!profiles[profile]) {
  throw new Error(`unsupported traffic profile: ${profile}`);
}

export const options = {
  scenarios: { traffic: profiles[profile] },
  tags: { source: 'sre-lab', run_id: runID, scenario, profile },
};

export default function () {
  const response = http.get(targetURL, {
    headers: { 'X-SRE-Run-ID': runID },
    redirects: 0,
  });
  check(response, { 'request succeeded': (result) => result.status >= 200 && result.status < 400 });
}
