import { useEffect, useState } from 'react';

type Props = { runID?: string; active: boolean };
type Values = { requestRate?: number; errorRate?: number; dependencyErrorRate?: number; p95?: number; cpu?: number };

async function query(expression: string): Promise<number | undefined> {
  const response = await fetch(`/prometheus/api/v1/query?query=${encodeURIComponent(expression)}`, { cache: 'no-store' });
  if (!response.ok) return undefined;
  const body = await response.json() as { data?: { result?: { value?: [number, string] }[] } };
  const raw = body.data?.result?.[0]?.value?.[1];
  if (raw === undefined) return undefined;
  const value = Number(raw);
  return Number.isFinite(value) ? value : undefined;
}

export function LiveMetrics({ runID, active }: Props) {
  const [metrics, setMetrics] = useState<Values>({});
  const [updatedAt, setUpdatedAt] = useState<number>();
  useEffect(() => {
    let stopped = false;
    const scope = runID ? `{run_id="${runID}"}` : '';
    const refresh = async () => {
      const [requestRate, errorRate, dependencyErrorRate, p95, cpu] = await Promise.all([
        query(`sum(rate(sre_lab_http_requests_total${scope}[1m]))`),
        query(`sum(rate(sre_lab_http_requests_total${scope ? `${scope.slice(0, -1)},code!="200"}` : '{code!="200"}'}[1m]))`),
        query(`sum(rate(sre_lab_dependency_errors_total${scope}[1m]))`),
        query(`histogram_quantile(0.95, sum(rate(sre_lab_http_request_duration_seconds_bucket${scope}[1m])) by (le))`),
        query('sum(rate(process_cpu_seconds_total{job=~"api-1|api-2"}[1m]))'),
      ]);
      if (!stopped) { setMetrics({ requestRate, errorRate, dependencyErrorRate, p95: p95 === undefined ? undefined : p95 * 1000, cpu }); setUpdatedAt(Date.now()); }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), active ? 5000 : 15000);
    return () => { stopped = true; clearInterval(timer); };
  }, [runID, active]);
  const value = (number: number | undefined, suffix = '') => number === undefined ? '—' : `${number < 10 ? number.toFixed(2) : Math.round(number)}${suffix}`;
  return <section className="live-metrics" aria-label="Live exercise metrics">
    <div><span>Request rate</span><strong>{value(metrics.requestRate, ' req/s')}</strong></div>
    <div><span>Error rate</span><strong>{value(metrics.errorRate, ' err/s')}</strong></div>
    <div><span>Dependency errors</span><strong>{value(metrics.dependencyErrorRate, ' err/s')}</strong></div>
    <div><span>P95 latency</span><strong>{value(metrics.p95, ' ms')}</strong></div>
    <div><span>API CPU</span><strong>{value(metrics.cpu)}</strong></div>
    <small className="metrics-updated">{updatedAt ? `Updated ${Math.max(0, Math.round((Date.now() - updatedAt) / 1000))}s ago` : 'Waiting for metrics'}</small>
  </section>;
}
