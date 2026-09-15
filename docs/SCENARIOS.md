# Scenario Authoring Contract

**The scenario engine provides strict installed-scenario parsing and validation.**
The loader reads `scenarios/**/*.yaml` and accepts the eleven executable catalog IDs
including the original three and the eight recommended follow-up exercises. The server exposes their
catalog, serialized start/reset/status, progressive hints, deterministic phase
controls and outcome checks; runtime state is kept behind fixed internal service
boundaries.

The browser now has a searchable learning index and an available introductory
walkthrough. Its display metadata and step-by-step instructions live separately
from the UI in `frontend/src/content/exercises.json`. These contain learner-facing
runbook copy, while executable scenario definitions remain in YAML and are served
by the runtime catalog API. The walkthrough checklist is explicitly self-reported;
incident checks use real traffic and observable outcomes.

The server persists the available walkthrough's checked indexes in a local named
volume. The UI restores that state when reopening the brief. This provides resume
behavior for the current shared lab, not learner identity, authoritative evidence
or a pass/fail result. Future scenario runs require run IDs and evidence scoped to
their own metric epoch; do not reuse this simple checklist record as grading data.

The executable `schema_version: 1` model is implemented in
`internal/scenario` and the installed definitions are in `scenarios/`. This is an
internal contract, not a published compatibility promise.

## Definition

| Field | Purpose |
| --- | --- |
| `schema_version`, `id` | Explicit format version and stable unique identity |
| `title`, `difficulty`, `description` | Learner-facing operational context |
| `learning_objectives` | Reasoning skills, not a required command sequence |
| `goal`, `success_criteria` | The outcome the learner must achieve and the observable conditions that prove it |
| `environment` | Required `docker` runtime and `api-dependency` topology |
| `traffic` | Discriminated constant/ramp/spike profile with bounded duration |
| `faults` | Desired failure/workload, target role, and typed bounded config |
| `objectives` | Availability and latency thresholds with explicit units |
| `grading` | Evaluation kind, sustained window, minimum samples/offered traffic |
| `hints` | Progressive evidence-first help, explicitly requested |

Constant traffic has only `rps` and `duration`; ramp has only from/to rates plus
ramp/hold durations; spike has only baseline/peak rates plus before/spike/after
durations. Rates must be positive and no more than 1000; every duration is greater
than zero and no more than one hour. Do not mix variants or encode k6 source in
scenario data.

Each fault has a `target`, `type`, and `config`. `cpu_work` applies only to `api`
and requires positive `config.work_units`. `error_rate` requires only
`config.probability`, greater than zero and no more than one. These model desired
exercise conditions, not commands or tool-specific settings. Unsupported fields,
types, targets, topology, runtime, grades, and objective units are validation
errors rather than ignored content.

Validation rejects unknown and duplicate fields, duplicate or unsupported IDs,
invalid targets, negative or excessive rates/durations, impossible probability
ranges, and runtime capability mismatches. It also requires all three installed
definitions. Scenario definitions are trusted installed content, not
learner-uploaded arbitrary files. Hidden faults must not leak through catalog APIs.

## Lifecycle and Evidence

Every executable run has two separate gates. First, the learner records a
diagnosis and a short evidence note; recovery controls remain locked until the
diagnosis matches the exercise's evidence model. Second, the grader evaluates
the real runtime outcome against `success_criteria`, including sustained
traffic, availability, latency, alert behavior, or the exercise-specific
forensic evidence. Commands are optional investigation tools, not completion
criteria.

Start validates first, restores baseline, waits for readiness, marks the metric
epoch and starts load. Reset stops traffic, clears faults/rules/capacity/hints,
closes terminal sessions and isolates fresh-run metrics. Partial startup failures
must be recoverable and reported, never labeled running.

For service recovery, evaluate real availability and p95 under maintained load
through a complete fresh window. Check generator health and client-side failures
as well as server counters. No traffic, stale data or insufficient samples yields
an inconclusive grade. Another valid capacity configuration must be allowed.

For alert lessons, validate expressions with promtool, then test behavior during
healthy/noisy, impaired and recovered phases. An always-firing or never-firing
rule must fail. Do not grade a rule by comparing its expression text to a solution.
Burn-rate exercises explicitly define the SLI denominator, SLO and window. Short
lab windows illustrate reasoning; they are not production paging prescriptions.

## Catalog

1. `cpu-saturation`: ramp/hold demand against real limited CPU; investigate,
    increase capacity, verify sustained recovery. Calibrate on real containers.
2. `useful-alerts`: harmless CPU fluctuations followed by actual user impairment;
   replace noise with actionable evidence-based alert behavior.
3. `slo-burn-rate`: deterministic failures, 99.9% availability SLO, error budget
   and rapid-burn alert behavior.
4. `vertical-horizontal`: compare vertical and horizontal capacity.
5. `dependency-bottleneck`: locate downstream service time.
6. `connection-pool`: diagnose concurrency saturation.
7. `latency-slo`: separate latency and availability objectives.
8. `dns-failure`: investigate service discovery failure.
9. `retry-storm`: stabilize amplified dependency failures.
10. `memory-leak`: identify growth and stable recovery.
11. `autoscaler-oscillation`: diagnose unstable feedback and cooldown.

Before shipping an exercise, demonstrate start, real traffic, visible evidence,
learner intervention, grading and reset in an end-to-end test. See ROADMAP.md for
the phase gates. Kubernetes, networking and KWOK need future providers and tests;
do not add placeholder features or claim synthetic nodes are real throughput.
