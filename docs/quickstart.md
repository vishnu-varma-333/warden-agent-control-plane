---
---

# Quickstart

Bring up dependencies:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

Run the gateway, control API, console and guard classifier (each in its
own terminal — see the root README for exact commands and first-time
setup, including the guard classifier's venv/training/ONNX export).

Jaeger UI for traces: [http://localhost:16686](http://localhost:16686)

## Try it

```bash
TOKEN=$(deploy/bench/get_token.sh)
curl -X POST localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" -H "X-Acting-As: user-1" \
  -H "Content-Type: application/json" \
  -d '{"model":"mock-model","messages":[{"role":"user","content":"hello"}]}'
```

Or open the console at [http://localhost:3000](http://localhost:3000) once
it's running (see `console/README.md` for its one-time setup).

## Deploying it for real

A single EC2 instance running the full stack as containers — see
`deploy/terraform/README.md` for the exact steps and an honest accounting
of what it actually costs.
