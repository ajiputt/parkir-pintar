# ECS task definitions & services.
# Loop via locals untuk DRY antar service.

locals {
  # search & presence: deferred (lihat docs/architecture/adr/0009).
  # Kontrak proto tetap di proto/search/v1/ & proto/presence/v1/.
  services = {
    gateway = {
      port = 8080, alb_attached = true
    }
    reservation  = { port = 9191, alb_attached = false }
    billing      = { port = 9192, alb_attached = false }
    payment      = { port = 9193, alb_attached = false }
    notification = { port = 9196, alb_attached = false }
  }
}

resource "aws_iam_role" "task_execution" {
  name = "parkir-task-exec-${var.environment}"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "secrets_read" {
  role = aws_iam_role.task_execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["secretsmanager:GetSecretValue", "rds:*"]
      Resource = "*"   # restrict di prod
    }]
  })
}

resource "aws_cloudwatch_log_group" "services" {
  for_each          = local.services
  name              = "/ecs/parkir-${var.environment}/${each.key}"
  retention_in_days = 14
}

resource "aws_ecs_task_definition" "service" {
  for_each                 = local.services
  family                   = "parkir-${each.key}-${var.environment}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.fargate_cpu
  memory                   = var.fargate_memory
  execution_role_arn       = aws_iam_role.task_execution.arn

  container_definitions = jsonencode([{
    name      = each.key
    image     = "${var.image_registry}/${each.key}:${var.image_tag}"
    essential = true
    portMappings = [{
      containerPort = each.value.port
      protocol      = "tcp"
    }]
    environment = concat([
      { name = "APP_ENV", value = var.environment },
      { name = "LOG_LEVEL", value = "info" },
      # NATS dideploy sebagai task terpisah dgn service discovery `nats.parkir.local`.
      { name = "NATS_URL", value = "nats://nats.parkir.local:4222" },
      { name = "RESERVATION_HTTP_URL", value = "http://reservation.parkir.local:9191" },
      { name = "BILLING_HTTP_URL",     value = "http://billing.parkir.local:9192" },
      { name = "PAYMENT_HTTP_URL",     value = "http://payment.parkir.local:9193" },
    ],
    # Tambahkan REDIS_ADDR hanya kalau enable_redis=true.
    # Reservation service auto-fallback ke memory locker kalau REDIS_ADDR kosong /
    # ping fail (lihat services/reservation/cmd/main.go).
    var.enable_redis ? [
      { name = "REDIS_ADDR", value = "${aws_elasticache_cluster.redis[0].cache_nodes[0].address}:6379" }
    ] : [])
    secrets = [
      { name = "DB_URL", valueFrom = "${aws_db_instance.postgres.master_user_secret[0].secret_arn}:url::" },
      { name = "JWT_SECRET", valueFrom = aws_secretsmanager_secret.jwt_secret.arn },
      { name = "MIDTRANS_SERVER_KEY", valueFrom = aws_secretsmanager_secret.midtrans_key.arn },
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.services[each.key].name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = each.key
      }
    }
    healthCheck = {
      command     = ["CMD-SHELL", "wget -q --spider http://localhost:${each.value.port}/healthz || exit 1"]
      interval    = 30
      timeout     = 5
      retries     = 3
      startPeriod = 30
    }
  }])
}

resource "aws_ecs_service" "service" {
  for_each        = local.services
  name            = each.key
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.service[each.key].arn
  desired_count   = var.service_replicas
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = module.vpc.private_subnets
    security_groups  = [aws_security_group.ecs_tasks.id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = each.value.alb_attached ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.gateway.arn
      container_name   = each.key
      container_port   = each.value.port
    }
  }

  service_registries {
    registry_arn = aws_service_discovery_service.svc[each.key].arn
  }
}

# ---- Service discovery (private DNS untuk inter-service) ----
resource "aws_service_discovery_private_dns_namespace" "main" {
  name = "parkir.local"
  vpc  = module.vpc.vpc_id
}

resource "aws_service_discovery_service" "svc" {
  for_each = local.services
  name     = each.key

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.main.id
    dns_records {
      ttl  = 10
      type = "A"
    }
    routing_policy = "MULTIVALUE"
  }
  health_check_custom_config { failure_threshold = 1 }
}
