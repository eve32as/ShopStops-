locals {
  stripe_secrets = var.enable_stripe ? [
    {
      name      = "STRIPE_SECRET_KEY"
      valueFrom = aws_secretsmanager_secret.stripe_secret_key[0].arn
    },
    {
      name      = "STRIPE_WEBHOOK_SECRET"
      valueFrom = aws_secretsmanager_secret.stripe_webhook_secret[0].arn
    },
    {
      name      = "STRIPE_MERCHANT_MONTHLY_PRICE_ID"
      valueFrom = aws_secretsmanager_secret.stripe_merchant_price[0].arn
    }
  ] : []
  service_secret_arns = concat(
    [
      aws_secretsmanager_secret.database_url.arn,
      aws_secretsmanager_secret.internal_api_token.arn
    ],
    var.enable_stripe ? [
      aws_secretsmanager_secret.stripe_secret_key[0].arn,
      aws_secretsmanager_secret.stripe_webhook_secret[0].arn,
      aws_secretsmanager_secret.stripe_merchant_price[0].arn
    ] : []
  )
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/ecs/${local.name}/api"
  retention_in_days = 90
  kms_key_id        = aws_kms_key.logs.arn
}

resource "aws_cloudwatch_log_group" "gateway" {
  name              = "/ecs/${local.name}/gateway"
  retention_in_days = 90
  kms_key_id        = aws_kms_key.logs.arn
}

resource "aws_cloudwatch_log_group" "migrator" {
  name              = "/ecs/${local.name}/db-migrator"
  retention_in_days = 90
  kms_key_id        = aws_kms_key.logs.arn
}

resource "aws_iam_role" "ecs_execution" {
  name = "${local.name}-ecs-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ecs_execution" {
  role       = aws_iam_role.ecs_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "ecs_secrets" {
  name = "${local.name}-read-service-secrets"
  role = aws_iam_role.ecs_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = local.service_secret_arns
    }]
  })
}

resource "aws_iam_role" "migrator_execution" {
  name = "${local.name}-migrator-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "migrator_execution" {
  role       = aws_iam_role.migrator_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "migrator_secrets" {
  name = "${local.name}-read-migration-secrets"
  role = aws_iam_role.migrator_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["secretsmanager:GetSecretValue"]
      Resource = [
        aws_db_instance.main.master_user_secret[0].secret_arn,
        aws_secretsmanager_secret.app_database_password.arn
      ]
    }]
  })
}

resource "aws_iam_role" "ecs_task" {
  name = "${local.name}-ecs-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_ecs_cluster" "main" {
  name = local.name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_service_discovery_private_dns_namespace" "main" {
  name        = "${var.project_name}.internal"
  description = "Private service discovery for ${local.name}"
  vpc         = aws_vpc.main.id
}

resource "aws_service_discovery_service" "api" {
  name = "api"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.main.id
    dns_records {
      ttl  = 10
      type = "A"
    }
    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
  }
}

resource "aws_ecs_task_definition" "api" {
  family                   = "${local.name}-api"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name                   = "api"
    image                  = var.api_image
    essential              = true
    readonlyRootFilesystem = true
    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]
    environment = [
      { name = "HTTP_ADDR", value = ":8080" },
    ]
    secrets = concat([
      { name = "DATABASE_URL", valueFrom = aws_secretsmanager_secret.database_url.arn },
      { name = "SHOPSTOPS_INTERNAL_API_TOKEN", valueFrom = aws_secretsmanager_secret.internal_api_token.arn }
    ], local.stripe_secrets)
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.api.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "api"
      }
    }
  }])
}

resource "aws_ecs_task_definition" "gateway" {
  family                   = "${local.name}-gateway"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name                   = "gateway"
    image                  = var.gateway_image
    essential              = true
    readonlyRootFilesystem = true
    portMappings = [{
      containerPort = 8081
      protocol      = "tcp"
    }]
    environment = [
      { name = "HTTP_ADDR", value = ":8081" },
      { name = "SHOPSTOPS_API_URL", value = "http://api.${aws_service_discovery_private_dns_namespace.main.name}:8080" },
      { name = "SHOPSTOPS_OIDC_ISSUER", value = var.oidc_issuer },
      { name = "SHOPSTOPS_OIDC_AUDIENCE", value = var.oidc_audience },
      { name = "SHOPSTOPS_OIDC_ROLE_CLAIM", value = var.oidc_role_claim },
      { name = "SHOPSTOPS_OIDC_ACTOR_CLAIM", value = var.oidc_actor_claim },
      { name = "SHOPSTOPS_GATEWAY_ALLOWED_ORIGINS", value = join(",", var.gateway_allowed_origins) }
    ]
    secrets = [
      { name = "SHOPSTOPS_INTERNAL_API_TOKEN", valueFrom = aws_secretsmanager_secret.internal_api_token.arn }
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.gateway.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "gateway"
      }
    }
  }])
}

resource "aws_ecs_task_definition" "migrator" {
  family                   = "${local.name}-db-migrator"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.migrator_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name                   = "db-migrator"
    image                  = var.migration_image
    essential              = true
    readonlyRootFilesystem = true
    environment = [
      { name = "PGHOST", value = aws_db_instance.main.address },
      { name = "PGPORT", value = "5432" },
      { name = "PGDATABASE", value = "shopstops" },
      { name = "PGUSER", value = "shopstops_admin" },
      { name = "PGSSLMODE", value = "verify-full" },
      { name = "PGSSLROOTCERT", value = "/etc/ssl/certs/rds-global-bundle.pem" },
      { name = "SHOPSTOPS_MIGRATIONS_DIR", value = "/opt/shopstops/migrations" }
    ]
    secrets = [
      {
        name      = "PGPASSWORD"
        valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:password::"
      },
      {
        name      = "SHOPSTOPS_DB_APP_PASSWORD"
        valueFrom = aws_secretsmanager_secret.app_database_password.arn
      }
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.migrator.name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "migrator"
      }
    }
  }])

  depends_on = [
    aws_iam_role_policy_attachment.migrator_execution,
    aws_iam_role_policy.migrator_secrets
  ]
}

resource "aws_ecs_service" "api" {
  count = var.deploy_application ? 1 : 0

  name                               = "api"
  cluster                            = aws_ecs_cluster.main.id
  task_definition                    = aws_ecs_task_definition.api.arn
  desired_count                      = 2
  launch_type                        = "FARGATE"
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  enable_execute_command             = false
  wait_for_steady_state              = true

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = [for subnet in aws_subnet.application : subnet.id]
    security_groups  = [aws_security_group.api.id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.api.arn
  }

  depends_on = [aws_iam_role_policy.ecs_secrets]
}

resource "aws_lb" "gateway" {
  name                       = substr("${local.name}-gateway", 0, 32)
  internal                   = false
  load_balancer_type         = "application"
  security_groups            = [aws_security_group.load_balancer.id]
  subnets                    = [for subnet in aws_subnet.public : subnet.id]
  drop_invalid_header_fields = true
  idle_timeout               = 60

  access_logs {
    bucket  = aws_s3_bucket.alb_access_logs.id
    prefix  = "alb"
    enabled = true
  }

  depends_on = [
    aws_s3_bucket_policy.alb_access_logs,
    aws_s3_bucket_server_side_encryption_configuration.alb_access_logs,
    aws_s3_bucket_ownership_controls.alb_access_logs
  ]
}

resource "aws_lb_target_group" "gateway" {
  name        = substr("${local.name}-gateway", 0, 32)
  port        = 8081
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id

  health_check {
    enabled             = true
    path                = "/v1/readyz"
    matcher             = "200"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.gateway.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.gateway.arn
  port              = 443
  protocol          = "HTTPS"
  certificate_arn   = var.certificate_arn
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"

  lifecycle {
    precondition {
      condition     = can(regex("^arn:aws[a-zA-Z-]*:acm:${var.aws_region}:[0-9]{12}:certificate/", var.certificate_arn))
      error_message = "The gateway TLS certificate must be issued in the same AWS region as the load balancer."
    }
  }

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}

resource "aws_ecs_service" "gateway" {
  count = var.deploy_application ? 1 : 0

  name                               = "gateway"
  cluster                            = aws_ecs_cluster.main.id
  task_definition                    = aws_ecs_task_definition.gateway.arn
  desired_count                      = 2
  launch_type                        = "FARGATE"
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  enable_execute_command             = false
  wait_for_steady_state              = true

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = [for subnet in aws_subnet.application : subnet.id]
    security_groups  = [aws_security_group.gateway.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.gateway.arn
    container_name   = "gateway"
    container_port   = 8081
  }

  depends_on = [
    aws_iam_role_policy.ecs_secrets,
    aws_lb_listener.https,
    aws_ecs_service.api
  ]
}

resource "aws_appautoscaling_target" "api" {
  count = var.enable_api_autoscaling && var.deploy_application ? 1 : 0

  max_capacity       = 10
  min_capacity       = 2
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.api[0].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_target" "gateway" {
  count = var.enable_api_autoscaling && var.deploy_application ? 1 : 0

  max_capacity       = 10
  min_capacity       = 2
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.gateway[0].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "api_cpu" {
  count = var.enable_api_autoscaling && var.deploy_application ? 1 : 0

  name               = "${local.name}-api-cpu"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.api[0].resource_id
  scalable_dimension = aws_appautoscaling_target.api[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.api[0].service_namespace

  target_tracking_scaling_policy_configuration {
    target_value       = 65
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

resource "aws_appautoscaling_policy" "gateway_cpu" {
  count = var.enable_api_autoscaling && var.deploy_application ? 1 : 0

  name               = "${local.name}-gateway-cpu"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.gateway[0].resource_id
  scalable_dimension = aws_appautoscaling_target.gateway[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.gateway[0].service_namespace

  target_tracking_scaling_policy_configuration {
    target_value       = 65
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}
