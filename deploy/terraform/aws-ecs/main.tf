# ParkirPintar — AWS ECS Fargate deployment.
# Budget-aware: ~$50/bulan untuk pola daily 10h × 22 hari kerja.
#
# Optimasi yang sudah dipakai:
#   1. NAT Instance (fck-nat t4g.nano) menggantikan NAT Gateway (hemat $29/bulan)
#   2. Skip ElastiCache Redis (reservation service auto-fallback memory locker;
#      hemat $16/bulan). Set var.enable_redis=true kalau perlu shared lock di
#      production multi-replica.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.50" }
  }
  backend "s3" {
    # Configure manually atau via -backend-config flag
    # bucket  = "parkir-tfstate"
    # key     = "parkir-pintar/terraform.tfstate"
    # region  = "ap-southeast-1"
    # encrypt = true
  }
}

provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project     = "parkir-pintar"
      Environment = var.environment
      ManagedBy   = "terraform"
      Owner       = "aji.perdana"
    }
  }
}

# ---- Networking ----
# NOTE: enable_nat_gateway = false. NAT instance di-handle module fck-nat di bawah,
# yang hemat $29/bulan dibanding managed NAT Gateway. Trade-off: throughput maks
# ~5 Gbps (cukup untuk demo & low-medium prod), single-point-of-failure di single AZ.
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.8.1"

  name = "parkir-${var.environment}"
  cidr = "10.10.0.0/16"
  azs  = ["${var.aws_region}a", "${var.aws_region}b"]

  public_subnets  = ["10.10.1.0/24", "10.10.2.0/24"]
  private_subnets = ["10.10.11.0/24", "10.10.12.0/24"]

  # Disable managed NAT Gateway → hemat $33/bulan; pakai fck-nat instance.
  enable_nat_gateway   = false
  enable_dns_hostnames = true
}

# ---- NAT Instance (fck-nat) — pengganti NAT Gateway ----
# t4g.nano (ARM Graviton) ~$3-4/bulan vs NAT Gateway $33/bulan.
#
# Trade-off:
#   ✅ Hemat $29/bulan (~30% dari total cost)
#   ✅ Cukup untuk demo & low-medium prod traffic
#   ⚠️  Single AZ → tidak HA. Untuk HA: ha_mode = true (pakai 2 instance).
#   ⚠️  Throughput max ~5 Gbps (NAT Gateway: 45 Gbps)
#   ⚠️  Self-managed (auto-restart pakai ASG, sudah handle module)
module "fck_nat" {
  source  = "RaJiska/fck-nat/aws"
  version = "~> 1.3"

  name          = "parkir-${var.environment}"
  vpc_id        = module.vpc.vpc_id
  subnet_id     = module.vpc.public_subnets[0]
  ha_mode       = false      # single AZ untuk demo budget
  instance_type = "t4g.nano" # ARM Graviton, ~$3/bulan

  update_route_tables = true
  route_tables_ids = {
    for idx, rtb in module.vpc.private_route_table_ids :
    "private-${idx}" => rtb
  }
}

# ---- Security Groups ----
resource "aws_security_group" "alb" {
  name   = "parkir-alb-${var.environment}"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "ecs_tasks" {
  name   = "parkir-tasks-${var.environment}"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 8080
    to_port         = 9300
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
    self            = true # service-to-service
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "rds" {
  name   = "parkir-rds-${var.environment}"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs_tasks.id]
  }
}

# ---- Redis Security Group (conditional) ----
# Hanya dibuat kalau var.enable_redis=true. Default: false (hemat $16/bulan).
resource "aws_security_group" "redis" {
  count  = var.enable_redis ? 1 : 0
  name   = "parkir-redis-${var.environment}"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs_tasks.id]
  }
}

# ---- RDS Postgres (small, demo budget) ----
resource "aws_db_subnet_group" "main" {
  name       = "parkir-${var.environment}"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_db_instance" "postgres" {
  identifier                  = "parkir-${var.environment}"
  engine                      = "postgres"
  engine_version              = "16.3"
  instance_class              = var.rds_instance_class
  allocated_storage           = 20
  storage_type                = "gp3"
  storage_encrypted           = true
  db_name                     = "parkirpintar"
  username                    = "parkir"
  manage_master_user_password = true
  db_subnet_group_name        = aws_db_subnet_group.main.name
  vpc_security_group_ids      = [aws_security_group.rds.id]
  backup_retention_period     = 7
  skip_final_snapshot         = var.environment != "prod"
  deletion_protection         = var.environment == "prod"
  publicly_accessible         = false
  apply_immediately           = var.environment != "prod"
}

# ---- ElastiCache Redis (CONDITIONAL — disabled by default) ----
#
# Default: var.enable_redis = false (hemat $16/bulan).
# Reservation service akan otomatis fallback ke pkg/lock.MemoryLocker
# (lihat services/reservation/cmd/main.go — graceful degradation pattern).
#
# KAPAN ENABLE:
#   - Production multi-replica reservation service (memory locker tidak shared)
#   - User-selected spot contention rate tinggi (butuh fast Redis lock)
#   - Distributed rate limit di gateway
#
# Set var.enable_redis = true di terraform.tfvars atau via -var flag.
resource "aws_elasticache_subnet_group" "main" {
  count      = var.enable_redis ? 1 : 0
  name       = "parkir-${var.environment}"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_elasticache_cluster" "redis" {
  count                = var.enable_redis ? 1 : 0
  cluster_id           = "parkir-${var.environment}"
  engine               = "redis"
  node_type            = var.redis_node_type
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
  engine_version       = "7.1"
  port                 = 6379
  subnet_group_name    = aws_elasticache_subnet_group.main[0].name
  security_group_ids   = [aws_security_group.redis[0].id]
}

# ---- ECS Cluster ----
resource "aws_ecs_cluster" "main" {
  name = "parkir-${var.environment}"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

# ---- ALB ----
resource "aws_lb" "main" {
  name               = "parkir-${var.environment}"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = module.vpc.public_subnets
}

resource "aws_lb_target_group" "gateway" {
  name        = "parkir-gateway-${var.environment}"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = module.vpc.vpc_id

  health_check {
    path                = "/healthz"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}

# ---- Secrets ----
resource "aws_secretsmanager_secret" "midtrans_key" {
  name = "parkir/${var.environment}/midtrans-server-key"
}

resource "aws_secretsmanager_secret" "jwt_secret" {
  name = "parkir/${var.environment}/jwt-secret"
}

# ---- ECS Task Definitions & Services (per service) ----
# Lihat ecs-services.tf untuk detail per-service.

output "alb_dns" {
  value = aws_lb.main.dns_name
}

output "rds_endpoint" {
  value = aws_db_instance.postgres.endpoint
}

output "redis_endpoint" {
  description = "Redis endpoint (kosong kalau Redis disabled)"
  value       = var.enable_redis ? aws_elasticache_cluster.redis[0].cache_nodes[0].address : ""
}

output "nat_instance_id" {
  description = "NAT instance ID (untuk debugging)"
  value       = module.fck_nat.instance_id
}
