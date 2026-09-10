# SRE Lab Roadmap

The initial work began at the Phase 1 execution gate. The scenario/runtime work
now implements the original three exercises plus the eight recommended follow-ups.

Phase 0, Phase 1, and the executable scenario/runtime phases are implemented and execution-tested. See
[docs/VERIFICATION.md](docs/VERIFICATION.md) for evidence and deployment limits.

| Phase | Deliverable | Required Evidence Before Advancing |
| --- | --- | --- |
| 0 - Design | Architecture, repository layout, scenario YAML contract, security and risk review | Coherent component/lifecycle/metrics/terminal/grading/reset boundaries documented |
| 1 - Skeleton | Go backend; React UI; Compose; Prometheus; API/dependency; toolbox PTY | `make up` works; UI says SRE Lab / System healthy based on live checks; browser terminal executes tools; real Prometheus targets healthy; unit and stack/browser smoke tests pass |
| 2 - Scenario Engine | Strict YAML loading/validation, catalog, start/reset/status, progressive hints | Parsing and invalid-capability tests; serialized lifecycle and failure cleanup tests; repeated resets return baseline |
| 3 - Metrics | Complete demo instrumentation and selected metric charts | Real traffic changes rate/errors/p95/CPU; missing/stale data clearly marked; collection/query tests |
| 4 - Traffic | Internal k6; reusable constant/ramp/spike arrival profiles | Offered traffic and client failures observable; profile integration tests; generator exhaustion detectable |
| 5 - CPU Exercise | Limited API capacity, actual CPU work, balancer, controlled scaling, grading | Implemented: real k6 traffic, CPU saturation, second warm API backend activation, sustained outcome check and reset |
| 6 - Alert Exercise | Noise/impact phases, validated read-only PromQL expression, behavior check | Implemented: deterministic incident/recovery phases and fire/clear evaluation after fresh scrapes |
| 7 - SLO Exercise | Valid-request SLI, 99.9% SLO, intermittent failure/burn-rate lesson | Implemented: deterministic error-budget incident, Prometheus evidence, expression fire/clear check |
| 8 - Polish | Error handling, contributor docs, UI clarity and full smoke coverage | New contributor can discover and run the full eleven-exercise catalog locally with one browser and no host lab tools |

## Phase 1 Checklist

- Create foundational health/demo metrics/terminal security tests with implementation.
- Pin direct dependencies and container tags; commit generated dependency lockfiles.
- Keep builds and tests containerized; no host Go, npm or troubleshooting installs.
- Include bootstrap/up/test/smoke/down/reset/clean and document data deletion.
- Build an actual browser smoke test for xterm input/output, resize/disconnect,
  live health, demo HTTP, and scraped metrics. No fake exercise or grading button.
- Validate default localhost startup independently of the optional private overlay.
- Record and verify the chosen hostname deployment. This operator chose no-auth
  existing Caddy ingress; external private-network enforcement was not independently
  audited, so do not claim an application-level access boundary.
- Record test evidence and limitations; keep future features behind explicit phase gates.

## MVP Completion Gate (Later)

A clean clone runs with `make up` at localhost:8080. A learner completes the
eleven-exercise catalog in the same UI, using real traffic, metrics and an embedded
toolbox terminal; changes the system; receives outcome-based grades; requests
progressive hints; and resets reliably. End-to-end tests cover start, traffic,
metric visibility, intervention, grading and reset. No cloud account or paid
service is needed. Do not claim this gate passed based on Phase 1 health tests.

## Deferred

Kubernetes/kind, KWOK, HPA/VPA/KEDA, networking fault providers, Alertmanager,
Grafana, tracing/log platforms, queues/databases, large-scale simulation, advanced
distributed failures. These require new exercises and capability implementations,
not placeholder MVP services. Authentication, multi-tenancy, SaaS, plugin markets,
gamification and AI-generated solutions are explicit non-goals.
