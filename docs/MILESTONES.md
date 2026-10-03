# Build milestones

Each milestone ends with something running and tested. Don't start the next
until the current one is done.

- [ ] **1. Foundations** — repository, CI pipeline, local Kubernetes, PostgreSQL, Redis, Kafka, OpenTelemetry wired up.
- [ ] **2. Model gateway** — streaming passthrough to 2+ providers, API keys, fallback, circuit breakers, exact caching. First latency benchmark.
- [ ] **3. Identity and rate limits** — OAuth with Keycloak, on-behalf-of tokens, distributed rate limits and budgets.
- [ ] **4. MCP gateway** — tool registry, proxying, definition pinning and change detection.
- [ ] **5. Policy engine** — Cedar policies, versioning, decision cache, decision logging.
- [ ] **6. Durable approvals** — approval state machine, webhook notifications, expiry, kill tests.
- [ ] **7. Audit log** — hash chain, signed checkpoints, verification command.
- [ ] **8. Guard classifier** — dataset, fine-tuning, ONNX service over gRPC, benchmark vs. LLM-as-judge.
- [ ] **9. Console** — tools, policies, approvals, audit and spend views.
- [ ] **10. Ship it** — AWS deploy with Terraform, full load and fault test reports, demo video, docs site, write-up.
- [ ] **11. Version 2** — delegation chains, just-in-time access, policy dry-run.

## Milestone 1 progress

- [x] Repository scaffolded (Go module, `cmd/gateway`, `cmd/control-api`, `services/guard-classifier`, `console`, `deploy/*`).
- [x] Local dev stack: Postgres, Redis, Redpanda, OTel Collector, Jaeger (`deploy/docker/docker-compose.yml`).
- [x] CI pipeline: build + vet + test on every push (`.github/workflows/ci.yml`).
- [ ] Local Kubernetes (kind/k3s) manifests for the same dependency stack.
- [ ] OpenTelemetry actually wired into gateway/control-api code (collector is up, but nothing emits spans yet).
