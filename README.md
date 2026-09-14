# SRE Lab

A self-hosted SRE / Platform Engineering training environment: an SRE flight
simulator for observing systems, collecting evidence, changing capacity and
verifying user-visible reliability. Open source under the MIT license.

**Current release: executable local exercise runtime.**
The local environment, real demo services, Prometheus, embedded toolbox terminal,
scenario catalog, deterministic k6 traffic, fault controls and outcome checks work.
The exercise index is grouped by category and the guided workspaces include
semantic resource diagrams and progressive tips.

## Quickstart

This is the complete path from a fresh machine to the first investigation.

Prerequisites:

- Docker Engine with Docker Compose v2.20+, Docker Desktop, or OrbStack.
- Make.
- At least 4 GB assigned to Docker and several GB of free disk space.
- Internet access for the first image and dependency downloads.

You do not need Go, Node, Prometheus, k6, or troubleshooting tools installed on
your host. The lab runs those inside containers and does not need cloud accounts,
credentials, or a SaaS service.

```sh
git clone https://github.com/Cesarsk/zth-sre.git
cd zth-sre
make bootstrap
make up
```

Open <http://localhost:8080>. You should see the **Exercise index**.

### Start Exercising

1. Open **Your First Investigation**.
2. Choose **Start walkthrough**.
3. Wait for **System healthy**.
4. Choose **Connect terminal**.
5. Run the commands shown beside the terminal.
6. Compare each result with its expected result, then check each completed step.

The walkthrough follows a real request through the API and dependency and then
finds that request in Prometheus. It is a self-check, not an automatic grade.
The server saves checklist state and investigation notes locally.

When you are ready for an incident, return to the index and open any available
exercise. Choose **Start exercise**, investigate the live fault with the terminal,
watch the live metric cards, apply the suggested intervention, and choose **Check
solution**. Each exercise uses real containers, generated traffic and Prometheus
evidence. There are twelve executable incident exercises covering capacity,
alerting, SLOs, dependencies, DNS, retries, memory and autoscaling.

Useful commands in the embedded terminal include:

```sh
curl -fsS http://api-lb:8080/ | jq
curl -fsS http://api-1:8080/metrics
dig api
ping -c 2 dependency
ss -tuna
```

The API makes a real HTTP request to the dependency. Requests populate real
Prometheus counters and histograms. **Open Prometheus** opens the read-only proxy
inside the same origin. Advanced users can also visit `http://localhost:9090`.
Try `up`, `sre_lab_http_requests_total`, and `rate(process_cpu_seconds_total[1m])`.
Exercise traffic starts only after selecting an incident exercise. **Reset exercise**
stops the runner and returns API capacity and fault state to baseline.

### Stop, Resume, Reset

```sh
make down       # stop containers; retain saved progress and metrics
make up         # resume the lab
make reset      # delete this lab's progress and metrics, then start clean
```

`make reset` is intentionally destructive for SRE Lab data only. It does not prune
unrelated Docker images, volumes, or projects. Use it when you want a clean incident
run or when a previous exercise was left active.

## Operations

| Command | Effect |
| --- | --- |
| `make bootstrap` | Build application/toolbox images and pull Prometheus |
| `make up` | Build and start the stack, waiting for container health checks |
| `make test` | Containerized Go race tests, vet, and Prometheus config validation |
| `make smoke` | Start the stack and run Chromium tests, including a real shell |
| `make down` | Stop this stack; retain Prometheus history |
| `make reset` | Remove this stack and its metrics volume; start a clean baseline |
| `make clean` | Remove this stack and its disposable metrics volume, without restarting |
| `make logs` | Follow bounded service logs |

`reset` and `clean` delete **only SRE Lab data**. Toolbox home and `/tmp` are
temporary; they are lost on container recreation. Never store valuable work there.
No command prunes unrelated Docker images, volumes or projects.

## Components

| Service | Role | Host Exposure |
| --- | --- | --- |
| `server` | Go API, compiled React UI, terminal and read-only metrics proxies | `127.0.0.1:8080` |
| `toolbox` | Non-root Bash PTYs and troubleshooting tools | None |
| `api-1`, `api-2` | Instrumented HTTP API pool members calling the dependency | None |
| `api-lb` | Fixed internal load balancer with controlled warm capacity | None |
| `traffic` | Isolated k6 runner controlled by the server | None |
| `dependency` | Instrumented HTTP dependency | None |
| `prometheus` | Five-second scrapes; two-hour/128 MB retention | `127.0.0.1:9090` |

Prometheus and local walkthrough progress have dedicated named volumes
(`sre-lab_metrics` and `sre-lab_progress`). Progress is a small atomically replaced
JSON record owned by the non-root server; it stores checked step indexes, notes and
an update timestamp. It is not an authentication, identity or grading system.
Prometheus configuration is a read-only bind mount. The toolbox and demos have neither host mounts nor
Docker socket access. Toolbox networks are internal; only the server and
Prometheus join the edge bridge needed for loopback publishing. All running
services are non-root, capability-dropped, resource-limited and read-only except
for explicit data/tmpfs mounts.

`GET /healthz` reports process liveness. `GET /api/status` checks four live
components and returns healthy/degraded. This is foundation readiness, not an SLO
or exercise grade. A service can be alive while requests fail.

## Terminal Boundaries

The terminal runs Bash **inside the toolbox**, never on the host. It supports
input, Ctrl-C, resize, disconnect and reconnect. Two simultaneous sessions are
allowed, with a 15-minute idle limit and one-hour maximum lifetime. Disconnect
terminates the shell process group. The shell is for trusted learners, not hostile
multi-tenancy; deliberately detached processes may survive until container reset.

Tools include bash, curl, wget, jq, dig, nslookup, ping, traceroute, ip, ss, ps, top,
free, vmstat, openssl and netcat. Ping uses unprivileged ICMP sockets, not NET_RAW.
Packet capture, Docker CLI, kubectl and yq are not installed; k6 runs in the
isolated traffic container and is not installed in the toolbox.
`top`/`ps` show toolbox processes, not API processes; `free`/`vmstat` can show Docker
VM-wide memory rather than container limits. Inspect API CPU via Prometheus.

The server checks Host and the exact browser Origin on terminal upgrades.
Forwarded headers do not grant permissions. No generic command or arbitrary-URL
proxy is exposed. These protections do not replace network access control.

## This Host

Deployment: **https://labs.mini.lucacesarano.com** through the existing Caddy
`private-ingress` network and private Tailscale DNS. The operator explicitly chose
**no application or ingress authentication**. All users who can reach the route
can open a toolbox terminal and share the same environment.

OrbStack translates source addresses before Caddy, so a source-IP tailnet rule
cannot reliably distinguish clients here. The deployment relies on external
private-network controls. Private DNS is not an access control; no independent
off-network firewall audit has been performed. Do not publish this service using
public DNS, router port forwarding, a tunnel or Tailscale Funnel.

The portable default stack does not depend on Caddy or Tailscale. The optional
host-specific overlay adds only the server to ingress and permits the exact
HTTPS browser origin in both terminal boundaries:

```sh
docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml up -d --build --wait server
```

Use the overlay consistently for operations on this deployment:

```sh
make up COMPOSE='docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml'
make reset COMPOSE='docker compose -f docker-compose.yml -f runtime/docker/compose.private.yml'
```

Running plain `make up` returns the stack to its localhost configuration and
removes the private overlay settings. Caddy routes to `sre-lab-server:8080`;
the Status catalog checks `/healthz`. Caddy credentials belong to the existing
ingress, not this project.

## Tests and Limitations

Go tests cover demo health, dependency behavior, metrics, status probes, static
file confinement, Host/Origin checks, proxy restrictions, and real Linux PTY
input/resize/lifecycle/session limits. Browser tests cover healthy/degraded UI,
terminal execution and reconnection, and real Prometheus scrapes. See
[Phase 1 verification](docs/VERIFICATION.md) for the audited gate.

Image tags and direct dependencies are pinned; Go sums and npm locks are tracked.
Debian security packages are resolved during image build, so rebuilds are not
bit-for-bit immutable. This is not an offline image distribution. The app is
single-learner and should not be deployed as an Internet-facing or production
service. No auth, cloud, Kubernetes, KWOK, tracing or heavy dependencies are hidden
behind the current UI.

Screenshot placeholder: a searchable exercise index leads to a guided workspace
with instructions, live component health, API-to-dependency topology and terminal.

## Roadmap

The executable catalog currently includes twelve exercises:

1. CPU saturation and horizontal scaling.
2. Noisy CPU alerts versus useful user-impact alerts.
3. Availability SLO and error-budget burn-rate reasoning.
4. Vertical versus horizontal scaling.
5. Dependency bottleneck.
6. Connection pool exhaustion.
7. Latency SLO versus availability SLO.
8. DNS failure.
9. Retry storm and cascading failure.
10. Memory leak and OOM recovery.
11. Autoscaler oscillation.
12. File handle forensics with `lsof`.

Read [ARCHITECTURE.md](ARCHITECTURE.md), [ROADMAP.md](ROADMAP.md),
[scenario design](docs/SCENARIOS.md), and [CONTRIBUTING.md](CONTRIBUTING.md).
The remaining exercise ideas are tracked in [TODO.md](TODO.md).
