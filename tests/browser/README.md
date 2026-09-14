# Browser Checks

Run against an already-started real lab. This package does not start or stop the
stack. `smoke.spec.ts` uses real HTTP, Prometheus and PTY connections;
`states.spec.ts` intercepts only status/terminal responses to test failure UI.

Use the exact Playwright image matching the package version:

```sh
docker run --rm --network sre-lab_control --ipc=host \
  -e BASE_URL=http://server:8080 \
  -v "$PWD/tests/browser:/work" -w /work \
  mcr.microsoft.com/playwright:v1.56.1-noble \
  sh -c 'npm ci && npm test'
```

Run from the repository root. Replace `sre-lab_control` with the actual Compose
network containing the server. `BASE_URL` defaults to `http://server:8080`.
Alternatively, use `--network host -e BASE_URL=http://127.0.0.1:8080` when host
networking is supported/enabled by your Docker runtime. That exact browser origin/Host
must be allowed by the Go server; do not weaken origin validation for tests.
All tested URLs are relative to `BASE_URL`.

For the private deployment, run `make smoke-private` after every redeploy. It
recreates the server and Toolbox with the private origin overlay before running
the suite, including a test that opens `/terminal` from an exercise over HTTPS.

For frontend-only checks (no Go/Compose stack), build the frontend as documented
in `frontend/README.md`, then run from the repository root:

```sh
docker run -d --name sre-lab-frontend-check \
  -v "$PWD/frontend:/work" -w /work node:22.14.0-bookworm-slim \
  npm run preview -- --port 4173 --strictPort
docker run --rm --network container:sre-lab-frontend-check --ipc=host \
  -e BASE_URL=http://127.0.0.1:4173 \
  -v "$PWD/tests/browser:/work" -w /work \
  mcr.microsoft.com/playwright:v1.56.1-noble \
  sh -c 'npm ci && npm test -- states.spec.ts'
docker rm -f sre-lab-frontend-check
```

These tests intercept status and WebSocket responses. They cover initial unknown
health, health failure transitions, invalid data, binary terminal rendering,
input/resize messages, explicit disconnect/reconnect, unexpected closure, and
mobile overflow. They are not evidence of real PTY execution or Go static serving.

The smoke test requires `/healthz`, the `/api/status` contract, binary PTY
output at `/terminal`, bash/curl/head/stty in toolbox, API `/` returning HTTP 200,
Prometheus text at API `/metrics`, and the read-only Prometheus query proxy at
`/prometheus/api/v1/query`. The `up` query must expose healthy scrape instances
`api-1:8080`, `api-2:8080` and `dependency:8080`. Terminal dimensions are checked against `stty
size`, not just browser geometry. Disconnect/reconnect must create a fresh shell.

Failures retain traces/screenshots in `test-results/`; the HTML report goes to
`playwright-report/`. Both are ignored. No CDN or remote browser service is used.

## Validation Evidence

Container validation on 2026-09-14: TypeScript/Vite production build passed;
all 16 browser tests passed against the rebuilt stack, including catalog
start/reset, real PTY, exercise Toolbox startup, Prometheus, persistence, mobile discovery, keyboard
explanations, command copying, per-exercise notes, and self-check labeling.
The three learner-experience tests also passed over HTTPS at
`labs.mini.lucacesarano.com`.

Exercise notes are stored per exercise in the learner's browser. The original
walkthrough notes and checklist continue to use the lab server's progress API.
Catalog completion badges use shared lab run history, and file-forensics
completion is explicitly labeled as a learner-confirmed self-check.
