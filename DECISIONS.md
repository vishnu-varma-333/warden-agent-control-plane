# Design decisions

Each entry: the options considered, what was chosen, why, and what it cost.
These double as interview talking points.

## 2026-10-03 — Monorepo, single Go module

**Options:** one Go module per service (`cmd/gateway`, `cmd/control-api` each
with their own `go.mod`, wired together via a `go.work` workspace) vs. a
single root module covering both binaries.

**Chose:** a single root module (`github.com/vishnu-varma-333/warden-agent-control-plane`)
with `cmd/gateway` and `cmd/control-api` as separate `main` packages, sharing
code from `internal/`.

**Why:** the gateway and control API are explicitly meant to share policy and
auth code. A single module makes that sharing free — no version pinning
between internal packages, one `go.mod` to keep tidy, one CI job to run. A
multi-module workspace only pays for itself once the services need
independent release cadences or diverge in dependency versions, neither of
which is true yet.

**Cost:** both binaries are forced onto the same Go version and the same
versions of shared third-party dependencies. If that becomes a real
constraint later (e.g. control-api needs a dependency upgrade the gateway
isn't ready for), this is the first thing to revisit — splitting a module
later is mechanical, so the decision isn't expensive to reverse.

## 2026-10-03 — Local dev via Docker Compose first, kind/k3s as a parallel track

**Options:** develop against a local Kubernetes cluster (kind/k3s) from day
one, as the original spec called for, vs. start with `docker-compose` for
dependencies and layer local Kubernetes manifests in alongside it.

**Chose:** `docker-compose` now (`deploy/docker/docker-compose.yml`) for
Postgres/Redis/Redpanda/OTel/Jaeger; `deploy/k8s-local/` is scaffolded but not
yet filled in, and is the next piece of milestone 1.

**Why:** the fastest useful iteration loop for writing gateway code right now
is `docker compose up` + `go run`. Kubernetes manifests for the same stack
are additive, not a replacement — they matter for later milestones
(approval kill-tests, rolling deploys) more than for today's "can I hit
Postgres from Go" loop. Building both at once would slow down getting the
first real code running.

**Cost:** there's a real gap between "works with docker-compose" and "works
on Kubernetes" (service discovery, config, resource limits) that will need
closing before the kill-test and rolling-deploy milestones. Tracked in
`docs/MILESTONES.md` under milestone 1 as still open.

**Update (same day):** closed the gap — `deploy/k8s-local/` holds plain
Kustomize manifests (no Helm yet; that's milestone 10) for the same five
services, applied to a real `kind` cluster and verified (all pods Ready,
PVCs bound) before tearing the cluster down. Two things changed going from
compose to Kubernetes: Redpanda's `--advertise-kafka-addr` has to be the
in-cluster DNS name (`redpanda.warden-local.svc.cluster.local`), not
`localhost`, since other pods resolve it through cluster DNS, not the docker
network; and Postgres's data volume is mounted with a `subPath` so the PVC's
root isn't handed to Postgres directly (it refuses to start if that
directory isn't empty on first init). `kind` clusters are disposable by
design — created, verified, deleted — same reasoning as the spec's "create
EKS only during benchmark runs" cost note, just applied locally.

## 2026-10-03 — OTel wiring: otelhttp middleware over manual spans

**Options:** hand-write middleware that starts/ends a span per request vs.
use `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`.

**Chose:** `otelhttp.NewHandler` wrapping the mux in both services
(`internal/telemetry` holds the shared TracerProvider setup; each binary's
`main.go` just calls `telemetry.Init(ctx, serviceName)`).

**Why:** this is the standard, maintained instrumentation path for Go's
`net/http` — it handles span naming, status codes, and context propagation
correctly, including edge cases (panics, hijacked connections) that a
hand-rolled version would get wrong first try. Writing it by hand would only
teach how *not* to do it.

**Cost:** one more third-party dependency in the hot path. Verified it
actually works end-to-end (not just "compiles"): ran the gateway against the
live collector, hit `/healthz` twice, queried Jaeger's API directly, and
confirmed two `gateway` spans arrived.
