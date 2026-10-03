# Warden — Agent Control Plane

A zero-trust authorization gateway that sits between AI agents and every tool
or model they call, and decides on each call whether it may proceed.

Problem: companies want agents to take real actions, but agents can be
tricked by prompt injection, use poisoned or silently changed tools, exceed
their authority, or run up costs — and most MCP deployments have no central
place to enforce permissions or prove what happened.

Warden authorizes every tool and model call based on the agent, the user it
acts for, and the context; blocks poisoned tools and prompt-injection
attempts; pauses risky actions until a human approves, durably across
restarts; records every decision in a tamper-evident audit log; and routes
model calls with fallback, budgets, and caching as supporting features.

## Repository layout

```
cmd/gateway/        Go — hot-path proxy for model + MCP tool calls
cmd/control-api/     Go — admin API behind the console (tools, policies, approvals, audit)
internal/            Go packages shared between gateway and control-api
services/guard-classifier/   Python — prompt-injection / tool-poisoning classifier (FastAPI + ONNX)
console/             Next.js admin UI
deploy/docker/       local dev stack (Postgres, Redis, Redpanda, OTel, Jaeger)
deploy/k8s-local/    manifests for local kind/k3s development
deploy/helm/         Helm chart for cluster deploys
deploy/terraform/    AWS infrastructure as code
docs/                design notes, benchmark reports
```

## Local development

Bring up dependencies:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

Run the gateway:

```bash
go run ./cmd/gateway
```

Run the control API (prints a random `ADMIN_TOKEN` on first boot if you
don't set one — the console needs this exact value):

```bash
go run ./cmd/control-api
```

Run the console (see `console/README.md` for first-time setup):

```bash
cd console && npm run dev
```

Run the guard classifier (see `services/guard-classifier/README.md` for first-time setup — venv, training, ONNX export):

```bash
cd services/guard-classifier && source .venv/bin/activate && python3 server.py
```

Jaeger UI for traces: http://localhost:16686

## Testing

```bash
go test ./...                          # unit + integration tests
deploy/fault/run_fault_tests.sh        # Toxiproxy fault tests (Redis loss, DB failover, failing provider)
go run ./cmd/killtest -n 100           # automated kill test (real process kills, not simulated)
python3 services/guard-classifier/security_test.py   # known injection/poisoning attack patterns
deploy/bench/run_throughput.sh         # load test: throughput per instance
```

Real, measured results for all of the above are in [docs/BENCHMARKS.md](docs/BENCHMARKS.md), not estimates.

## Running the full stack in containers

```bash
docker build -f deploy/docker/Dockerfile.gateway -t warden-gateway .
docker build -f deploy/docker/Dockerfile.control-api -t warden-control-api .
docker build -f deploy/docker/Dockerfile.demo-mcp-server -t warden-demo-mcp-server .
docker build -t warden-console ./console
docker build -t warden-guard-classifier ./services/guard-classifier   # needs model/ present — see that dir's README
deploy/docker/generate_env_secrets.sh
docker compose -f deploy/docker/docker-compose.prod.yml --env-file deploy/docker/.env up -d
```

Deploying this to AWS (one EC2 instance, not EKS — see DECISIONS.md for
why): [deploy/terraform/README.md](deploy/terraform/README.md), including
an honest cost breakdown.

## Status

Build milestones and their state are tracked in [docs/MILESTONES.md](docs/MILESTONES.md).
Design decisions and the reasoning behind them are in [DECISIONS.md](DECISIONS.md).
Operational guidance (what each failure mode means, what to do about it) is in [RUNBOOK.md](RUNBOOK.md).
The project write-up is in [WRITEUP.md](WRITEUP.md).
A browsable docs site lives in `docs/` (quickstart, architecture, security model, benchmarks) — enable GitHub Pages (Settings → Pages → Deploy from branch → `main` / `docs`) to serve it.
