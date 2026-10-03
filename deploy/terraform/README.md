# AWS deploy

One EC2 instance running the full stack via Docker Compose — not EKS. See
`main.tf`'s header comment for why (no AWS free tier for the EKS control
plane; the spec's own cost note reserves EKS for benchmark runs, not the
always-on demo). No RDS, no ElastiCache, no load balancer: Postgres,
Redis and Redpanda run as containers on the same VM, same as local dev.

## Cost reality, stated plainly

- **EC2** (`t3.micro`): free for 750 hrs/month for 12 months on a new AWS
  account; otherwise roughly $7-8/month on-demand. But see the sizing
  note in `variables.tf` — this stack (Keycloak's JVM + the guard
  classifier's loaded model + everything else) is genuinely tight on
  1GB of RAM. `t3.small` (2GB, **not** free-tier eligible, ~$15/month)
  is a safer floor if you hit OOM kills; `t3.medium` (4GB, ~$30/month)
  is comfortable.
- **EBS** (20GB gp3): a few cents/month, usually within the free tier's
  30GB.
- **Elastic IP**: free while attached to a running instance; AWS charges
  for an EIP that's allocated but NOT attached to anything running, so
  don't `terraform destroy` the instance while leaving the EIP around.
- Nothing else — no NAT gateway, no load balancer, no managed DB/cache.

Set a billing alarm in AWS Budgets before applying this, regardless.

## Prerequisites

1. An AWS account with credentials configured locally — `aws configure`,
   or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` env vars. Verify with
   `aws sts get-caller-identity`. Never paste these into a chat with an
   AI assistant or anywhere else — configure them directly in your own
   terminal.
2. Terraform >= 1.5.
3. An SSH key pair (`ssh-keygen -t ed25519` if you don't have one).

## Deploy

```bash
cd deploy/terraform
terraform init
terraform plan -var="public_key_path=~/.ssh/id_ed25519.pub"
# review the plan, then:
terraform apply -var="public_key_path=~/.ssh/id_ed25519.pub"
```

This only provisions the VM and installs Docker — it does not build or
run anything (see `main.tf`'s comment on why: the guard classifier's
model directory can't be fetched by `git clone`, so there's no automation
that both works unattended and is honest about that). After `apply`:

```bash
# from the repo root, upload the repo INCLUDING the trained model
# (services/guard-classifier/model/, which is gitignored and must exist
# on your machine already — see that directory's README if it doesn't)
rsync -avz --exclude='.git' --exclude='console/node_modules' --exclude='console/.next' \
  . ubuntu@$(terraform -chdir=deploy/terraform output -raw public_ip):~/warden/

ssh ubuntu@$(terraform -chdir=deploy/terraform output -raw public_ip)
cd ~/warden
deploy/docker/generate_env_secrets.sh
docker build -f deploy/docker/Dockerfile.gateway -t warden-gateway .
docker build -f deploy/docker/Dockerfile.control-api -t warden-control-api .
docker build -f deploy/docker/Dockerfile.demo-mcp-server -t warden-demo-mcp-server .
docker build -t warden-console ./console
docker build -t warden-guard-classifier ./services/guard-classifier
docker compose -f deploy/docker/docker-compose.prod.yml --env-file deploy/docker/.env up -d
```

Console at `http://<public_ip>:3000`, gateway at `:8080`, Keycloak token
endpoint at `:8180`.

## A real bug this surfaced (documented, not hidden)

Getting `docker-compose.prod.yml` running at all — the exact thing this
VM now runs — surfaced a live Keycloak issue: a token's `iss` claim
reflected whatever hostname/port the client used to reach Keycloak
(`localhost:8180` from outside, `keycloak:8080` from another container),
which didn't match the gateway's fixed `KEYCLOAK_ISSUER` and made every
token fail with "invalid issuer". Fixed by pinning `KC_HOSTNAME` to a
full URL (`http://keycloak:8080`, Keycloak 25's "hostname:v2" provider
needs scheme+port in that one variable — `KC_HOSTNAME_PORT` is a v1-only
option and is silently ignored under v2). This is why the compose file's
`keycloak` service has that env var and a comment pointing back here —
the public IP a real client uses to fetch a token still resolves to the
same pinned issuer, so this isn't specific to local testing.

## Teardown

```bash
terraform destroy -var="public_key_path=~/.ssh/id_ed25519.pub"
```
