# Contributing

SRE Lab is at Phase 1. Read ARCHITECTURE.md and ROADMAP.md before changing its
scope. The goal is systems reasoning and evidence-based operations, not command
memorization. Keep the first three exercises excellent before growing a catalog.

## Workflow

1. Describe the operational problem and acceptance evidence in an issue or PR.
2. Prefer small changes with explicit behavior over plugin frameworks or generic layers.
3. Add tests with the implementation; never interpret missing telemetry as success.
4. Run `make test` and `make smoke`; both execute tooling inside containers. `make smoke` must pass after every redeploy and includes a test that starts an exercise and connects the Toolbox terminal during run startup.
5. Update architecture and contributor docs when contracts or deployment change.

Only Docker/Compose and Make are required on the host. Build the UI with
`docker compose build server`. Go uses one module; frontend and browser tests have
separate npm locks. Do not add global host tool installation requirements.

The Go tests run as non-root because the toolbox refuses root execution. They
include Linux PTY integration and the race detector. Browser smoke tests share
the server network namespace and use localhost, avoiding relaxed Host/Origin
checks solely for tests. They execute actual shell commands and verify scrapes.

For formatting:

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.26.1-bookworm go fmt ./...
```

Update dependency locks through a container, review the resulting diff, and
rebuild/retest. Keep direct versions pinned. Never commit `.env` files, tokens,
private keys, metrics data, node_modules or browser artifacts.

## Quality and Scope

Scenario data describes educational intent; providers implement mechanics.
Reject unsupported capabilities rather than pretending they work. Keep runtime
operations scoped to one lab project and validate inputs before side effects.
Grade observed reliability or alert behavior, not exact commands, replica counts,
YAML fragments or expression strings. Future simulated scale must be explicitly
distinguished from real capacity.

Preserve the security boundary: no host shell, Docker socket in the web server,
privileged toolbox, arbitrary proxy, external asset CDN or public terminal.
Treat network isolation as defense in depth, not a hostile-user sandbox.
Changes to the terminal deserve disconnect, timeout and resource-limit tests.

The default stack must run without private ingress. Host-specific overlays must
not become product dependencies. Do not modify an operator's ingress, auth or
network exposure implicitly. `make reset`/`clean` affect only disposable lab data.

Contributions are under the MIT license. Include verification commands and
observed results in a PR; be explicit about tests that were not run.
