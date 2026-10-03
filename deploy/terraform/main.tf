# One EC2 instance running the full stack via docker compose
# (deploy/docker/docker-compose.prod.yml) — not EKS. The spec names "EKS
# or EC2" as acceptable choices, and its own cost note says to run the
# always-on demo on a single small VM and reserve EKS for benchmark runs
# only, then destroy it. EKS's control plane alone has no AWS free tier
# (~$73/month to exist, running or not), so for a demo that needs to be
# cheap or free, EC2 is the only one of the two that actually fits that
# goal — see DECISIONS.md. No RDS, no ElastiCache, no load balancer: this
# VM runs the same containers docker-compose.prod.yml runs locally,
# avoiding three more billable AWS services for a single-instance demo.
#
# Deliberately kept to the default VPC (no custom VPC/subnets/NAT gateway
# — a NAT gateway alone costs real money per hour even sitting idle,
# which is exactly the kind of cost a single-VM demo shouldn't carry).

data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical
  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_key_pair" "demo" {
  key_name   = var.key_name
  public_key = file(var.public_key_path)
}

resource "aws_security_group" "demo" {
  name        = "warden-demo"
  description = "Warden single-VM demo: SSH + the app's own ports"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.ssh_allowed_cidr]
  }

  ingress {
    description = "gateway, control-api, console, Keycloak"
    from_port   = 3000
    to_port     = 8180
    protocol    = "tcp"
    cidr_blocks = [var.app_allowed_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "demo" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  key_name               = aws_key_pair.demo.key_name
  subnet_id              = data.aws_subnets.default.ids[0]
  vpc_security_group_ids = [aws_security_group.demo.id]

  root_block_device {
    volume_size = 20 # gp3; the guard-classifier image alone is ~1GB, plus Postgres/Redpanda data
    volume_type = "gp3"
  }

  # Installs Docker + the compose plugin only. It deliberately does NOT
  # clone the repo, build images, or run `docker compose up` — the guard
  # classifier's model/ directory (~512MB) is gitignored and isn't in any
  # git clone (see services/guard-classifier/Dockerfile), so "build on the
  # VM via git clone" wouldn't actually work unattended anyway. The real
  # steps (upload the repo including model/, generate .env, build, up)
  # are a short, explicit, human-run sequence — see this directory's
  # README — not hidden inside automation that would silently fail on
  # the one file that can't be fetched this way.
  user_data = <<-EOF
    #!/bin/bash
    set -e
    apt-get update
    apt-get install -y docker.io docker-compose-plugin
    systemctl enable --now docker
    usermod -aG docker ubuntu
  EOF

  tags = {
    Name = "warden-demo"
  }
}

resource "aws_eip" "demo" {
  instance = aws_instance.demo.id
  domain   = "vpc"
}
