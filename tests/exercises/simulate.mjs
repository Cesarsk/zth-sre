#!/usr/bin/env node

const base = (process.env.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const full = process.argv.includes('--full');
const headers = { 'content-type': 'application/json', origin: base };
const wait = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

const cases = [
  ['cpu-saturation', 'api-capacity', ['capacity', { activeBackends: 2 }], 'dependency', 65000],
  ['useful-alerts', 'user-impact', ['alert', { expression: 'sum(increase(sre_lab_http_requests_total{code=~"5.."}[1m]))' }], 'api-capacity', 0],
  ['slo-burn-rate', 'error-budget-burn', ['alert', { expression: 'sum(increase(sre_lab_http_requests_total{code=~"5.."}[1m]))' }], 'user-impact', 0],
  ['vertical-horizontal', 'api-capacity', ['capacity', { activeBackends: 2 }], 'dependency', 65000],
  ['dependency-bottleneck', 'dependency', ['intervention', { action: 'dependency-recovery' }], 'api-capacity', 65000],
  ['connection-pool', 'concurrency', ['intervention', { action: 'pool-recovery' }], 'api-capacity', 65000],
  ['latency-slo', 'latency', ['intervention', { action: 'latency-budget' }], 'dependency', 65000],
  ['dns-failure', 'service-discovery', ['intervention', { action: 'dns-recovery' }], 'dependency', 65000],
  ['retry-storm', 'retry-amplification', ['intervention', { action: 'retry-budget' }], 'dependency', 65000],
  ['memory-leak', 'memory-growth', ['intervention', { action: 'replace-capacity' }], 'api-capacity', 65000],
  ['autoscaler-oscillation', 'feedback-loop', ['intervention', { action: 'stabilize-policy' }], 'api-capacity', 65000],
  ['file-forensics', 'file-owner', ['file-evidence', {}], 'memory-growth', 0],
  ['blocked-traffic', 'network-policy', ['intervention', { action: 'network-policy-recovery' }], 'service-discovery', 65000],
];

async function request(path, method = 'POST', body = {}) {
  const response = await fetch(`${base}${path}`, {
    method,
    headers,
    body: method === 'GET' ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let value;
  try { value = JSON.parse(text); } catch { value = { error: text.trim() }; }
  return { status: response.status, value };
}

async function action(kind, payload) {
  if (kind === 'alert') return request('/api/run/alert', 'PUT', payload);
  return request(`/api/run/${kind}`, 'POST', payload);
}

async function runCase([id, diagnosis, [kind, payload], wrongDiagnosis, recoveryWait]) {
  await request('/api/run/reset');
  const startedWrong = await request(`/api/scenarios/${id}/start`);
  if (startedWrong.status !== 201) throw new Error(`${id}: wrong-path start returned ${startedWrong.status}`);
  const wrongRecorded = await request('/api/run/diagnosis', 'POST', {
    diagnosis: wrongDiagnosis,
    evidence: `Plausible but incorrect evidence for ${id} that must not unlock the mitigation.`,
  });
  if (wrongRecorded.status !== 200) throw new Error(`${id}: wrong diagnosis was not recordable`);
  const wrongAction = await action(kind, payload);
  if (wrongAction.status === 200) throw new Error(`${id}: wrong diagnosis unlocked ${kind}`);

  await request('/api/run/reset');
  const started = await request(`/api/scenarios/${id}/start`);
  if (started.status !== 201) throw new Error(`${id}: correct-path start returned ${started.status}`);
  const recorded = await request('/api/run/diagnosis', 'POST', {
    diagnosis,
    evidence: `Observed scenario-specific evidence for ${id} before applying the mitigation.`,
  });
  if (recorded.status !== 200) throw new Error(`${id}: correct diagnosis returned ${recorded.status}`);

  if (full && (id === 'useful-alerts' || id === 'slo-burn-rate')) {
    const alert = await action(kind, payload);
    if (alert.status !== 200) throw new Error(`${id}: alert returned ${alert.status}`);
    await wait(8000);
    if ((await request('/api/run/phase', 'POST', { phase: 'incident' })).status !== 200) throw new Error(`${id}: incident phase failed`);
    await wait(15000);
    if ((await request('/api/run/phase', 'POST', { phase: 'recovery' })).status !== 200) throw new Error(`${id}: recovery phase failed`);
    await wait(75000);
  } else {
    const correctAction = await action(kind, payload);
    if (correctAction.status !== 200) throw new Error(`${id}: correct ${kind} returned ${correctAction.status}`);
    if (full && recoveryWait) await wait(recoveryWait);
  }

  if (full) {
    const check = await request('/api/run/check');
    if (!check.value.passed) throw new Error(`${id}: full grade failed: ${check.value.feedback || 'no feedback'}`);
  }
  await request('/api/run/reset');
  return { id, wrongAction: wrongAction.status, passed: full };
}

for (const testCase of cases) {
  const result = await runCase(testCase);
  process.stdout.write(`${JSON.stringify(result)}\n`);
}
