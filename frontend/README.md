# SRE Lab Frontend

React/TypeScript and xterm.js with local assets only. Go serves `frontend/dist`;
Vite is build/development tooling, not the production server. The UI contains a
searchable catalog, guided walkthrough, executable incident runbooks, runtime
controls, Prometheus links and the shared API-pool topology.

From the repository root, build without installing anything on the host:

```sh
docker run --rm -v "$PWD/frontend:/work" -w /work \
  node:22.14.0-bookworm-slim sh -c 'npm ci && npm run build'
```

Direct dependencies are exact versions; the lockfile is generated in Docker.
Use the same image with `npm install --package-lock-only --ignore-scripts` after
deliberately changing a dependency pin, then validate with `npm ci` and the build.

## Server Contracts

- `GET /api/status`: JSON `{status: 'healthy' | 'degraded', components: [{name: 'api' | 'dependency' | 'prometheus' | 'toolbox', status: 'healthy' | 'unavailable'}], phase: 1}`. Exactly one entry for each component is required. Overall healthy requires all four healthy. Polling is every five seconds after completion, with an eight-second request timeout. Initial health is unknown; failed, timed-out or invalid responses clear previously reported health.
- `/terminal`: same-origin `ws:` or `wss:` WebSocket. Input is text JSON `{type: 'input', data: string}`; geometry is text JSON `{type: 'resize', cols: number, rows: number}`. Server output must be binary PTY bytes. Connect/disconnect is explicit, unexpected closure is visible, reconnect starts a new shell, and ResizeObserver drives xterm fit and PTY resizing.
- `GET /healthz`: process liveness only; never used to claim system health.
- `/prometheus/`: same-origin read-only proxy, linked from the UI.
- `GET /api/scenarios`: sanitized executable scenario catalog.
- `GET /api/run`: current single-run state.
- `POST /api/scenarios/{id}/start`, `/api/run/reset`, `/api/run/check`: lifecycle and outcome checks.
- `POST /api/run/phase`, `/api/run/capacity`, `/api/run/hint`, `PUT /api/run/alert`: bounded exercise controls.

See `../tests/browser/README.md` for containerized browser checks and live-stack
requirements. Building the frontend alone does not validate Go static serving or
the real toolbox/Prometheus integrations.
