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

Run the control API:

```bash
go run ./cmd/control-api
```

Run the guard classifier (see `services/guard-classifier/README.md` for first-time setup — venv, training, ONNX export):

```bash
cd services/guard-classifier && source .venv/bin/activate && python3 server.py
```

Jaeger UI for traces: http://localhost:16686

## Status

Build milestones and their state are tracked in [docs/MILESTONES.md](docs/MILESTONES.md).
Design decisions and the reasoning behind them are in [DECISIONS.md](DECISIONS.md).
