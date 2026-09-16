# SRE Lab Architecture

Status: executable local runtime implemented; future Kubernetes/networking
contracts below are designs, not claims of working exercises.

## Principles and Scope

Outcome over commands. Evidence before action. User impact matters. Scaling is
not always the answer. Execute real behavior where affordable. Present one
platform, not a collection of unrelated tools.

SRE Lab is a single-learner, local training environment. Docker and Compose are
the only runtime prerequisites; Make is a convenience. No cloud, SaaS, Kubernetes,
database, authentication product, plugin system, or telemetry service is needed.
The MVP exercise catalog contains thirteen executable scenarios: the original CPU,
alerting and SLO exercises plus eight capacity, dependency, networking and
reliability follow-ups. The first-investigation walkthrough remains a
non-incident onboarding exercise.

## Repository Structure

```text
sre-lab/
  cmd/server/                 Go HTTP control plane and static UI entrypoint
  cmd/toolbox/                In-container PTY/WebSocket agent
  cmd/demo/                   Entrypoint for API and dependency roles
  internal/server/            Health, HTTP boundaries, terminal proxy
  internal/terminal/          Bounded shell sessions, resizing, cleanup
  internal/demo/              Small instrumented HTTP service
  frontend/                  React + TypeScript + xterm.js, built with Vite
  toolbox/Dockerfile          Troubleshooting tools; no Docker socket
  observability/prometheus/  Scrape config; rules added in later phases
  tests/                     Real stack and browser smoke tests
  docs/SCENARIOS.md           Scenario authoring contract (initially design)
  runtime/docker/            Optional private deployment overlay
  Dockerfile                 Multi-stage UI/Go builds and test targets
  docker-compose.yml         Portable local stack
  Makefile                   bootstrap/up/down/reset/test/clean
  ARCHITECTURE.md
  ROADMAP.md
  README.md
  CONTRIBUTING.md
```

Phase 2 adds `internal/scenario/` and `scenarios/{cpu-saturation,alerting,slo-burn-rate}/`.
Phase 4 adds `load/` and a k6 adapter. Create directories when code needs them,
not as empty scaffolding. One Go module, one frontend package, no monorepo tooling.

## Components and Docker Topology

```text
browser -> localhost:8080 -> server (Go + compiled UI)
                              | health/read-only metrics proxy
                              +---------------------> Prometheus
                              | WebSocket
                              +-----> toolbox agent -> bash PTY

toolbox -> api -> dependency
Prometheus -> api-1:8080/metrics, api-2:8080/metrics, dependency:8080/metrics

later: k6 -> load-balancer -> api replicas -> dependency
```

Use distinct internal networks: control (server + toolbox), observation (server
+ Prometheus), and lab (server + toolbox + Prometheus + API + dependency). No lab ports
are published. The server publishes only `127.0.0.1:8080`; Prometheus publishes
only `127.0.0.1:9090` for advanced inspection. Toolbox has no external network.
Server and Prometheus also join an edge bridge to enable their loopback port
bindings; Docker internal-only networking suppresses host port reachability.
The private deployment overlay connects only the server to existing ingress.
Images run as non-root, drop capabilities, limit CPU/memory/PIDs and use read-only
filesystems with bounded tmpfs for necessary writes. No host filesystem or Docker
socket is available to the learner or browser-facing service.

## Backend and Frontend

The backend owns trusted scenario loading, serialized lifecycle operations,
capability checks, hint progress, bounded metrics queries, grading, and terminal
proxying. The frontend renders returned data; it never decides grades or embeds
scenario definitions. A single Go process serves the compiled React application.
React uses local state and effects, not a global state-management framework.
All assets including fonts and terminal code are served locally, never from a CDN.

Core API:

| Endpoint | Contract |
| --- | --- |
| `GET /healthz` | Process liveness, not a claim all dependencies are healthy |
| `GET /api/status` | Bounded checks of toolbox, API, dependency and Prometheus; overall healthy/degraded |
| `GET /terminal` | Same-origin WebSocket upgrade to toolbox, never a host shell |
| `GET /prometheus/…` | Read-only proxy to fixed Prometheus upstream |

Runtime API:

| Endpoint | Contract |
| --- | --- |
| `GET /api/scenarios` | Sanitized catalog for the installed YAML scenarios |
| `GET /api/run` | Current single-run state and phase |
| `POST /api/scenarios/{id}/start` | Validate, baseline, configure faults and start bounded k6 |
| `POST /api/run/reset` | Stop traffic and restore capacity/fault baseline |
| `POST /api/run/capacity` | Select one or two fixed API pool members during CPU exercise |
| `POST /api/run/phase` | Select baseline, incident or recovery for alert/SLO exercises |
| `PUT /api/run/alert` | Store and validate a bounded read-only PromQL expression |
| `POST /api/run/check` | Evaluate fresh Prometheus evidence and return feedback |
| `POST /api/run/hint` | Reveal the next progressive hint |

Future API: metrics series, richer alert-rule evaluation and provider-specific
controls. Mutations require same-origin
requests and explicit JSON content types. No generic exec, URL fetch, container
create, file path, or arbitrary Compose arguments are accepted.

## Embedded Terminal

xterm.js opens a same-origin WebSocket through the server to an agent inside the
toolbox. The agent starts non-root `/bin/bash` in a Linux PTY using `creack/pty`.
Input messages are JSON `{type: "input", data: "..."}` or
`{type: "resize", cols: 120, rows: 30}`; output is binary terminal data.
Validate lengths and geometry. Limit concurrent sessions, bound idle/lifetime,
clean up the process group and PTY on disconnect. UI offers explicit connect and
disconnect, shows errors, and fits terminal dimensions with ResizeObserver.

Tools: bash, curl, wget, jq, dig, nslookup, ping, traceroute, ip, ss, ps, top,
free, vmstat, openssl and netcat. Packet capture capabilities are deliberately not
granted. Commands run only in toolbox; `top` shows toolbox processes, not API CPU.
Use API process metrics for saturation evidence. Terminal is not a security
sandbox for hostile multi-tenant users. Network isolation prevents routine
toolbox egress to the host/Internet; Docker daemon administrators remain trusted.

## Scenario Lifecycle (Implemented Runtime)

`idle -> starting -> running -> stopping -> idle`, with an explicit `error` state.
Each start/reset creates a run ID and timestamp. Only one active run exists.
Lifecycle operations are serialized and idempotent where possible. Validate the
entire definition and provider capabilities before side effects. Startup orders:
stop old traffic, restore baseline, install initial rules/faults, wait for health
and discovery, mark metric epoch, start traffic. Partial failure stops traffic
and reports a recoverable error; it never reports running after failed startup.
On control-plane restart, reconcile or stop an orphan run before accepting work.

Hints are progressive and explicitly requested. Grading never reveals a solution.
Status includes phase, start time, revealed hints, topology and operation errors.

The Phase 1 introductory walkthrough stores self-reported checked steps and
learner notes in a small server-owned JSON record on a named volume. Writes are
bounded, same-origin JSON requests and atomically replace the file. This supports
returning to the shared local lab after refresh or a normal restart. It is not an
identity system, an evidence store or a future grading record; reset removes it.

## Scenario YAML Model (Proposed v1)

```yaml
schema_version: 1
id: cpu-saturation-01
title: API CPU Saturation
difficulty: beginner
description: |
  Users report increasing latency. Collect evidence and restore reliability.
learning_objectives:
  - Identify saturation and capacity headroom
  - Verify a mitigation with user-visible outcomes
environment:
  runtime: docker
  topology: api-dependency
  capacity:
    api: {replicas: 1, cpu_limit: 0.25}
traffic:
  profile: ramp
  from_rps: 10
  to_rps: 60
  ramp_duration: 60s
  hold_duration: 15m
faults:
  - target: api
    type: cpu_work
    work_units: 1000000
objectives:
  availability: 0.999
  p95_latency_ms: 500
grading:
  kind: service_recovery
  window: 60s
  minimum_requests: 1000
  minimum_offered_rps: 55
hints:
  - Compare traffic, latency and errors before changing anything.
  - Compare API CPU usage with its allocated CPU capacity.
  - Try increasing API capacity, then observe the recovery period.
```

Rates/work units above are provisional; calibrate on real limited CPU containers
before shipping exercise 1. Validate unknown fields, IDs, unique catalog IDs,
positive bounded durations/rates, probability ranges, topology targets, objective
units, and runtime/grade/fault capabilities. Reject unsupported types explicitly.
No shell commands, PromQL, k6 source, arbitrary mounts or implementation-specific
network fault tools in scenario YAML. Each installed scenario is trusted content;
learner-submitted alert expressions are a separate bounded input.

Constant uses `rps` and `duration`; ramp uses from/to/ramp/hold duration; spike uses
baseline/peak/before/spike/after durations. These are discriminated variants, not
a bag of optional fields. Future faults describe links and effects, e.g.
`type: network_latency, source: api, target: dependency, latency: 200ms`.
Do not silently pretend an unimplemented fault works. Hidden fault configuration
must not be returned by the learner-facing catalog/status API.

## Metrics and Traffic

Prometheus owns storage, scraping and rule evaluation. Start with five-second
scrapes and a short retention window. Real demo metrics include requests by
bounded status labels, latency histograms in seconds, active requests, dependency
latency/errors and Go process CPU. Health and metrics endpoints are excluded
from request SLIs. The runtime adds bounded, named backend queries for rate,
errors, p95, CPU and availability/burn-rate evidence. Missing data is shown as
unknown.
Advanced users can open the embedded proxy or localhost Prometheus directly.

The SRE Lab frontend forbids inline scripts. The fixed Prometheus proxy permits
its bundled UI's inline initialization script under a path-scoped CSP exception;
the terminal and application pages retain the stricter script policy.

The implemented traffic runner translates conceptual traffic profiles into a reusable k6 script using
arrival-rate executors. k6 runs internally, no host installation. Track offered
traffic, completions, client failures and dropped iterations; API success counters
alone cannot see requests that timed out before reaching the service. Never pass
an exercise because traffic stopped. Each load run has a bounded duration and
resource budget; exhausted generator capacity is an invalid evaluation, not a
successful service. A steady hold after a ramp gives time to investigate.

## Grading and Three Minimal Exercises

Grading follows observe, hypothesize, change, verify. A check collects the full
post-start, post-warmup window, verifies freshness/coverage, offered load and
minimum samples, then evaluates objective thresholds and health. Insufficient
history or unavailable telemetry is inconclusive, never passing. Return observed
values, required values and actionable feedback. Stability must hold across
subwindows, not just a favorable final sample or long-window average.

1. CPU saturation: real bounded CPU work, queue/admission limit and deadline;
   fixed CPU quota, ramp/hold traffic and an actual load balancer with replica
   discovery. A narrow capacity API/toolbox `lab` command scales API capacity.
   Grade availability and latency under maintained offered load, not replica count.
2. Useful alert: deterministic CPU variation without user impact, followed by
   latency/errors. Learner submits a Prometheus rule via toolbox file/command or
   UI. Validate with promtool before atomic installation. Evaluate the learner
   rule against repeatable healthy/noisy and impaired phases: no healthy page,
   timely firing during user impact and recovery clearing. Do not accept any
   always-firing alert or merely check that the rule file contains error metrics.
3. SLO burn rate: same API and rule-edit path, deterministic intermittent failures,
   explicit valid-request denominator, 99.9% availability SLO and recording rules.
   Burn rate = error ratio / 0.001. Controlled low/rapid burn and recovery phases
   test alert behavior; never compare a learner expression to one exact string.
   Single-window lesson first, with labels/window dimensions allowing later
   multi-window evaluations. Short lab windows are not production paging advice.

## Reset Strategy

Phase 1 `make reset` recreates only this Compose project and clears its disposable
Prometheus volume and toolbox tmpfs. `make down` preserves metrics; `make clean`
explicitly removes lab data. No global prune or unrelated project changes.
Phase 2 reset stops traffic, restores baseline capacity/config/faults/rules, clears
hints/grading state, and starts a new epoch. Either clear metrics or restrict every
grading query to new-run data with sufficient fresh history. Reset closes terminal
sessions so background processes cannot contaminate the next run.

## Security and Private Hosting

Default management bindings are loopback. Validate Host and exact browser Origin
on terminal upgrades to resist DNS rebinding/cross-site WebSocket hijacking.
No forwarded header changes the allowed Origin list; configure exact origins.
No arbitrary proxies, Docker socket, privileged containers or host PID/network.
Toolbox and demo services have no ingress-network membership or published ports.

`labs.mini.lucacesarano.com` is an optional private deployment using the existing
Caddy/Tailscale ingress, not an open-source product dependency. Private DNS alone
does not restrict access: Caddy currently binds all interfaces. Apply a route-level
tailnet peer restriction where original client addresses are available; do not
trust spoofable X-Forwarded-For. On this host OrbStack rewrites the source to its
gateway. The operator explicitly selected existing Caddy ingress without
authentication after this limitation was demonstrated. Thus this deployment
relies on external private-network controls, not a per-route access gate. Private
DNS alone is not enforcement, and an off-network audit was not performed.
No authentication product is in scope. All reachable users share one lab state
and can execute toolbox commands. Internet deployment is unsupported.

## Provider Boundaries and Risks

Add small interfaces only as implementations need them: runtime lifecycle/capacity,
traffic start/stop/status, named metrics, fault apply/clear. Grading composes evidence;
it need not become a plugin. The future Docker controller is internal and exposes
allowlisted lab-project actions; the socket remains root-equivalent even read-only.
Design/review that boundary before Phase 5, never add a socket to this web server.

Kubernetes maps topology roles/capacity/readiness to its resources without changing
educational objectives. Do not expose Compose service names as scenario semantics.
Networking faults target topology edges, implemented later by tc/Toxiproxy adapters.
KWOK requires capability metadata distinguishing simulated nodes/scheduling from
real request execution; it cannot satisfy physical throughput/latency objectives.

Key risks: CPU variability across workstations; unreliable Docker DNS balancing;
per-process CPU mistaken for quota saturation; traffic-generator bottlenecks;
missing data incorrectly graded as success; stale samples surviving reset; mutable
rules leaking between runs; exposed host control; and testing alert syntax rather
than alert behavior. Phase gates require evidence for each before catalog expansion.
