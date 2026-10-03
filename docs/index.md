---
title: Warden — Agent Control Plane
---

# Warden

A zero-trust authorization gateway that sits between AI agents and every
tool or model they call, and decides on each call whether it may proceed.

Agents can be tricked by prompt injection, use poisoned or silently
changed tools, exceed their authority, or run up costs — and most MCP
deployments have no central place to enforce permissions or prove what
happened. Warden authorizes every call based on the agent, the user it
acts for, and the context; blocks poisoned tools and injection attempts;
pauses risky actions for human approval, durably across restarts; records
every decision in a tamper-evident audit log; and routes model calls with
fallback, budgets, and caching as supporting features.

## Start here

- **[Quickstart](quickstart.html)** — run it locally in a few commands.
- **[Architecture](ARCHITECTURE.html)** — the request path, what fails
  open vs. closed, and why.
- **[Security model](SECURITY.html)** — the threat model and what
  mechanism answers each threat.
- **[Benchmarks](BENCHMARKS.html)** — real, measured numbers: latency,
  throughput, the guard classifier's accuracy, fault and kill test
  results — not estimates.
- **[Build milestones](MILESTONES.html)** — how this was actually built,
  in order, including the real bugs found at each stage.

## Source

[github.com/vishnu-varma-333/warden-agent-control-plane](https://github.com/vishnu-varma-333/warden-agent-control-plane)
