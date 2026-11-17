resource "aws_ecs_cluster" "runtime" {
  name = var.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

locals {
  environment = {
    api = { HTTP_ADDR = ":8080", OSRM_URL = "http://osrm.shopstops.internal:5001" }
    gateway = {
      HTTP_ADDR                     = ":8080"
      SHOPSTOPS_BACKEND_URL         = "http://api.shopstops.internal:8080"
      SHOPSTOPS_OIDC_ISSUER         = var.oidc_issuer
      SHOPSTOPS_OIDC_AUDIENCE       = var.oidc_audience
      SHOPSTOPS_OIDC_TRUSTED_CLAIMS = tostring(var.oidc_trusted_claims)
      SHOPSTOPS_CORS_ORIGINS        = "https://${var.domain}"
    }
    admin = {}
    osrm = {
      AWS_REGION              = var.aws_region
      EXPECTED_AWS_ACCOUNT_ID = var.expected_aws_account_id
      OSRM_ARTIFACT_BUCKET    = var.osrm_artifact_bucket
      OSRM_DATASET_VERSION    = var.osrm_dataset_version
      OSRM_DATA_PARENT        = "/data/osrm"
      HOME                    = "/tmp"
    }
  }
  mounts = {
    api     = []
    gateway = []
    admin   = [{ sourceVolume = "scratch", containerPath = "/tmp", readOnly = false }]
    osrm    = [{ sourceVolume = "scratch", containerPath = "/tmp", readOnly = false }, { sourceVolume = "dataset", containerPath = "/data/osrm", readOnly = false }]
  }
  logging = { for service in local.services : service => {
    logDriver = "awslogs"
    options = {
      awslogs-group         = aws_cloudwatch_log_group.service[service].name
      awslogs-region        = var.aws_region
      awslogs-stream-prefix = service
      mode                  = "non-blocking"
      max-buffer-size       = "4m"
    }
  } }
  # Fargate cannot add CHOWN or mount tmpfs. The short-lived root init keeps
  # only CHOWN from Docker's default capability set; main containers drop ALL.
  init_drop = ["DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "SETGID", "SETUID", "SETPCAP", "NET_BIND_SERVICE", "NET_RAW", "SYS_CHROOT", "MKNOD", "AUDIT_WRITE", "SETFCAP"]
  init = { for service in ["admin", "osrm"] : service => {
    name             = "volume-init", image = var.images[service], essential = false
    user             = "0:0", readonlyRootFilesystem = true
    entryPoint       = ["/bin/sh", "-ec"]
    command          = [service == "osrm" ? "mkdir -p /data/osrm /tmp; chmod 0700 /data/osrm /tmp; chown 10001:10001 /data/osrm /tmp" : "mkdir -p /tmp; chmod 0700 /tmp; chown 101:101 /tmp"]
    mountPoints      = local.mounts[service]
    linuxParameters  = { capabilities = { drop = local.init_drop } }
    logConfiguration = local.logging[service]
  } }
  health = {
    admin = {
      command  = ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1"]
      interval = 30, timeout = 5, retries = 3, startPeriod = 30
    }
    osrm = {
      # Listener liveness only; real routing is a separate read-only test.
      command  = ["CMD", "/bin/bash", "-ec", "exec 3<>/dev/tcp/127.0.0.1/5001"]
      interval = 60, timeout = 5, retries = 10, startPeriod = 300
    }
  }
}

resource "aws_ecs_task_definition" "service" {
  for_each                 = local.services
  family                   = "${var.name}-${each.key}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = each.key == "osrm" ? "2048" : "512"
  memory                   = each.key == "osrm" ? "8192" : "1024"
  execution_role_arn       = aws_iam_role.execution[each.key].arn
  task_role_arn            = each.key == "osrm" ? var.osrm_task_role_arn : aws_iam_role.application[each.key].arn
  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }
  dynamic "ephemeral_storage" {
    for_each = each.key == "osrm" ? [1] : []
    content {
      size_in_gib = var.osrm_ephemeral_storage_gib
    }
  }
  dynamic "volume" {
    for_each = contains(["admin", "osrm"], each.key) ? ["scratch"] : []
    content {
      name = volume.value
    }
  }
  dynamic "volume" {
    for_each = each.key == "osrm" ? ["dataset"] : []
    content {
      name = volume.value
    }
  }
  container_definitions = jsonencode(concat(
    contains(["admin", "osrm"], each.key) ? [local.init[each.key]] : [],
    [merge({
      name                   = each.key, image = var.images[each.key], essential = true
      user                   = each.key == "admin" ? "101:101" : "10001:10001"
      readonlyRootFilesystem = true
      linuxParameters        = { capabilities = { drop = ["ALL"] } }
      portMappings           = [{ containerPort = each.key == "osrm" ? 5001 : 8080, protocol = "tcp" }]
      environment            = [for name, value in local.environment[each.key] : { name = name, value = value }]
      secrets                = local.secrets[each.key]
      mountPoints            = local.mounts[each.key]
      logConfiguration       = local.logging[each.key]
      stopTimeout            = 60
      }, try({
        dependsOn   = [{ containerName = "volume-init", condition = "SUCCESS" }]
        healthCheck = local.health[each.key]
    }, {}))]
  ))
  depends_on = [aws_iam_role_policy.execution]
}

resource "aws_ecs_service" "service" {
  for_each                           = local.services
  name                               = each.key
  cluster                            = aws_ecs_cluster.runtime.id
  task_definition                    = aws_ecs_task_definition.service[each.key].arn
  desired_count                      = each.key == "osrm" ? var.osrm_desired_count : 2
  launch_type                        = "FARGATE"
  platform_version                   = "1.4.0"
  enable_execute_command             = false
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  health_check_grace_period_seconds  = contains(["gateway", "admin"], each.key) ? 120 : null
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }
  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.service[each.key].id]
    assign_public_ip = false
  }
  dynamic "service_registries" {
    for_each = contains(["api", "osrm"], each.key) ? [each.key] : []
    content {
      registry_arn = aws_service_discovery_service.private[each.key].arn
    }
  }
  dynamic "load_balancer" {
    for_each = contains(["gateway", "admin"], each.key) ? [each.key] : []
    content {
      target_group_arn = aws_lb_target_group.public[each.key].arn
      container_name   = each.key
      container_port   = 8080
    }
  }
  # No migration runner or migration dependency is invented here.
  depends_on = [aws_lb_listener.https, aws_lb_listener_rule.gateway, aws_vpc_security_group_ingress_rule.service, aws_vpc_security_group_egress_rule.service, aws_vpc_security_group_egress_rule.database, aws_vpc_security_group_egress_rule.aws_https, aws_vpc_security_group_egress_rule.gateway_https]
}