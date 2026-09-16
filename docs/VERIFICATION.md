# SRE Lab Verification

Audited on 2026-09-10 using Docker Engine 29.4.0 / Compose v5.1.2 on OrbStack
(Linux amd64 containers on macOS). This verifies the Phase 1 foundation and the
initial executable runtime for all thirteen exercises. It is not a claim of production
alerting or cold autoscaling fidelity.

## Deliverable Checklist

| Requested Deliverable | Artifact / Observed Evidence |
| --- | --- |
| Inspect existing repository | No prior SRE Lab project; new checkout under `/Users/mini/Documents/projects/sre-lab`; existing private Caddy/Status conventions inspected |
| Architecture before implementation | ARCHITECTURE.md and ROADMAP.md created before code; component, lifecycle, terminal, metrics, grading, reset and security boundaries recorded |
| Concrete repository structure | ARCHITECTURE.md tree; one Go module, React package, toolbox image, observability config and Compose stack |
| YAML model and thirteen exercises | ARCHITECTURE.md, docs/SCENARIOS.md, strict loader tests, and thirteen installed YAML definitions |
| Future Kubernetes/networking/KWOK risks | ARCHITECTURE.md provider/risk section; no unimplemented provider is advertised as working |
| Go backend + frontend | cmd/server, internal/server, frontend; production TypeScript/Vite build completed in Docker |
| Real API and dependency | cmd/demo, internal/demo; API actually calls dependency; real health and Prometheus metrics tested |
| One-command local startup | `make up` launches server, toolbox, API pool, load balancer, dependency, traffic runner and Prometheus; `/api/status` returned healthy with four live components |
| Embedded terminal | Browser executed a unique Bash marker and curl against API; PTY size matched browser resize; reconnect created a fresh shell |
| Toolbox tools | All 17 documented tools resolved inside container; UID 1000; ping reached API without added capabilities |
| Metrics storage | Prometheus v3.5.0 scraped `api-1`, `api-2` and dependency with `up=1`; promtool accepted config |
| Prometheus browser access | Browser opened same-origin proxy and found Execute control without page errors; fixed inline-bootstrap CSP issue found during audit |
| Test commands | `make test`: Go race tests and vet passed, promtool passed; `make smoke`: 12/12 Chromium tests passed |
| Private hostname | HTTPS status returned healthy; 8/8 catalog/live browser tests passed at labs.mini.lucacesarano.com after reset, including WebSocket shell and Prometheus UI |
| Bootstrap | `make bootstrap` completed image builds and Prometheus pull |
| Clean/reset | `make reset` invoked clean then up; toolbox sentinel disappeared; metrics volume creation timestamp changed; stack returned healthy |
| Safe default exposure | Only server 127.0.0.1:8080 and Prometheus 127.0.0.1:9090 published; no toolbox/API/dependency published port |
| Isolation | Non-root shell, no Docker socket, read-only agent binary, internal toolbox networks; external numeric-IP HTTP request failed |
| Existing deployment integration | Caddy validated and gracefully reloaded; only Status restarted for catalog; Status entry observed HTTP 200 / up |
| Documentation / licensing | README.md, ARCHITECTURE.md, ROADMAP.md, CONTRIBUTING.md, docs/SCENARIOS.md, MIT LICENSE |

## Tests Actually Cover

`internal/demo`: healthy and failed upstreams, timeout/cancellation, real request
metrics, latency/active requests and exclusion of probes from SLIs.

`internal/server`: parallel status probes, degraded response, exact Host/Origin
checks, fixed-upstream terminal and Prometheus proxy, method/path/redirect guards,
static confinement and scoped CSP exception.

`internal/terminal`: real Linux PTY execution/resizing, disconnect and child-group
cleanup, maximum sessions, malformed input, idle/lifetime bounds and shutdown.

`tests/browser`: real stack health, binary terminal output, shell commands,
resize/disconnect/reconnect, real scrapes, actual Prometheus UI, and mocked
startup/degraded/error frontend states. Mocked state tests are not substituted
for the live stack tests.

## Reproduce

```sh
make test
make smoke
make reset
```

For this host's optional private configuration:

```sh
make smoke COMPOSE='docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml'
docker compose --profile test run --rm --no-deps \
  -e BASE_URL=https://labs.mini.lucacesarano.com browser-test npm run test:smoke
make reset COMPOSE='docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml'
```

No remote publication, git commit, push or PR was requested or performed.

## Limits and Explicit Deferrals

### Guided Workspace Follow-Up

Added a searchable exercise index and a five-step first-investigation walkthrough
with an objective, commands, expected observations, questions and a self-check
list. The incident entries now open executable runbooks backed by the scenario
runtime; the first walkthrough remains a self-check rather than an automatic grade.

The updated browser suite passed 11/11 tests: catalog search/availability,
workspace navigation/deep links/history, checklist reset, narrow-screen layout,
CPU exercise start/reset, and the existing live terminal, health and Prometheus tests.
After deployment, all 7 catalog/live smoke tests also passed through
`https://labs.mini.lucacesarano.com` with normal TLS verification.

### Persistence and Exercise Briefs Follow-Up

The progress API permits only same-origin `PUT` JSON to a bounded exercise ID;
it normalizes checked indexes, bounds notes to 16 KB and atomically replaces the
local record. The UI serializes rapid progress writes so older requests cannot
overwrite newer notes. Unit tests cover persistence, normalization, invalid inputs and
cross-origin write rejection. Browser coverage saves checkboxes and notes,
reloads and restores both. A manual server recreation also restored saved step
indexes from the named volume before the test record was cleared.

The thirteen executable catalog entries now have situation, objective, learning objectives,
investigation steps, hover/focus answers and progressive hints. CPU start, warm
capacity activation and reset were exercised through the browser; the service
runtime also exposes deterministic alert and SLO phase controls.

The eight follow-up exercise pages were browser-checked for category placement,
resource topology diagrams, start controls and consultable hints. All eight
scenario IDs were also started and reset through the runtime API.

`make reset` was also run after writing a checked step and notes. It removed the
`sre-lab_progress` volume, created a new one, returned an empty progress record,
and restored all four live status components through the HTTPS hostname.

The operator explicitly chose no authentication behind the existing Caddy
ingress after source-IP translation was demonstrated. This is not a verified
per-user or per-route access boundary: every reachable user can execute toolbox
commands. Private DNS alone provides no security; an off-network exposure audit
was not possible from this local test. Do not publish this service publicly.

The shell is restricted by Docker configuration, not a hostile-user sandbox.
Detached processes can require a full reset. TLS was verified against the real
hostname; no browser certificate-error bypass was used.

The runtime uses a fixed internal k6 runner and a two-member API warm pool; it
does not claim cold Compose autoscaling. Alert exercises evaluate learner-supplied
read-only PromQL against deterministic incident/recovery phases rather than
installing arbitrary Prometheus rule files. A full long-duration production-grade
alert test and polished metric-chart view remain follow-up work.

Runtime resource usage is workstation-dependent. One post-reset sample totaled
about 58 MiB for the five containers, excluding Docker VM/build/test overhead;
configured runtime memory caps total 768 MiB. This is not a capacity benchmark.
