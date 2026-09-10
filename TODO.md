# Exercise Backlog

The current catalog contains the onboarding walkthrough, the original three
exercises, and the eight recommended exercises. These remaining ideas are
intentionally backlog items until their runtime providers and grading contracts
are designed.

## Capacity & Scaling

- [ ] Load balancer imbalance
- [ ] Capacity headroom planning
- [ ] Queue backlog
- [ ] Cache failure and hit-ratio collapse

## Monitoring & Alerting

- [ ] Missing Prometheus scrape target
- [ ] Alert cardinality explosion
- [ ] Dashboard that hides a per-instance incident
- [ ] Alert recovery failure
- [ ] Noisy periodic alert with duration and rate tuning

## SLO & Reliability

- [ ] Multi-window burn rate
- [ ] Error-budget release policy
- [ ] Partial instance or region outage
- [ ] Dependency SLO composition

## Networking

- [ ] Network latency and queueing
- [ ] Packet loss and retry interaction
- [ ] TLS certificate or protocol failure
- [ ] TCP connection exhaustion

## Distributed Systems

- [ ] Thundering herd
- [ ] Circuit breaker tuning
- [ ] Cascading failure
- [ ] Retry budget enforcement

Each backlog item needs a declarative scenario, a semantic resource topology,
progressive tips, deterministic fault provider, observable success criteria, reset
behavior and an end-to-end smoke test before it is promoted to the catalog.
