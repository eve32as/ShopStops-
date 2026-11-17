# Provider is mocked: no AWS credentials, API calls, state backend or resources.
mock_provider "aws" {}

variables {
  expected_aws_account_id   = "246813579024"
  aws_region                = "us-east-1"
  vpc_id                    = "vpc-0123456789abcdef0"
  private_subnet_ids        = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]
  public_subnet_ids         = ["subnet-0123456789abcdef2", "subnet-0123456789abcdef3"]
  rds_security_group_id     = "sg-0123456789abcdef0"
  database_url_secret_arn   = "arn:aws:secretsmanager:us-east-1:246813579024:secret:runtime/database-AbCd12"
  internal_token_secret_arn = "arn:aws:secretsmanager:us-east-1:246813579024:secret:runtime/token-EfGh34"
  certificate_arn           = "arn:aws:acm:us-east-1:246813579024:certificate/12345678-1234-1234-1234-123456789abc"
  domain                    = "live.shopstops.com"
  oidc_issuer               = "https://identity.shopstops.com/tenant"
  oidc_audience             = "shopstops-api"
  osrm_task_role_arn        = "arn:aws:iam::246813579024:role/shopstops-osrm-runtime-reviewed"
  osrm_artifact_bucket      = "shopstops-staging-osrm-246813579024-us-east-1"
  osrm_dataset_version      = "ny-20261114"
  aws_https_egress_cidrs    = ["10.0.10.0/24", "10.0.11.0/24"]
  images = {
    api     = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/api@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    gateway = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/gateway@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    admin   = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/admin@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
    osrm    = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/osrm@sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
  }
}

run "private_services_and_edge" {
  command = plan
  assert {
    condition = (
      alltrue([for service in aws_ecs_service.service : service.network_configuration[0].assign_public_ip == false && service.platform_version == "1.4.0" && service.deployment_circuit_breaker[0].rollback]) &&
      aws_ecs_service.service["api"].desired_count == 2 && aws_ecs_service.service["gateway"].desired_count == 2 &&
      aws_ecs_service.service["admin"].desired_count == 2 && aws_ecs_service.service["osrm"].desired_count == 1
    )
    error_message = "Private Fargate replica/platform/rollback defaults changed."
  }
  assert {
    condition = (
      aws_lb_target_group.public["gateway"].health_check[0].path == "/healthz" &&
      aws_lb_target_group.public["admin"].health_check[0].path == "/healthz" &&
      aws_lb_listener.https.protocol == "HTTPS" && aws_lb_listener.http.default_action[0].redirect[0].protocol == "HTTPS" &&
      length(aws_wafv2_web_acl.rate) == 1 && aws_wafv2_web_acl.rate[0].rule != null
    )
    error_message = "HTTPS, health or default WAF contract changed."
  }
  assert {
    condition = (
      aws_vpc_security_group_ingress_rule.service["gateway_api"].from_port == 8080 &&
      aws_vpc_security_group_ingress_rule.service["api_osrm"].from_port == 5001 &&
      length(aws_vpc_security_group_ingress_rule.service) == 4 &&
      aws_service_discovery_private_dns_namespace.internal.name == "shopstops.internal"
    )
    error_message = "Service-only ingress/discovery contract changed."
  }
  assert {
    condition = alltrue([for service in aws_ecs_task_definition.service :
      alltrue([for container in jsondecode(service.container_definitions) : container.readonlyRootFilesystem])
    ])
    error_message = "All containers, including init, must have read-only root filesystems."
  }
  assert {
    condition = (
      jsondecode(aws_ecs_task_definition.service["osrm"].container_definitions)[0].user == "0:0" &&
      jsondecode(aws_ecs_task_definition.service["osrm"].container_definitions)[1].user == "10001:10001" &&
      jsondecode(aws_ecs_task_definition.service["osrm"].container_definitions)[1].dependsOn[0].condition == "SUCCESS" &&
      jsondecode(aws_ecs_task_definition.service["admin"].container_definitions)[1].dependsOn[0].condition == "SUCCESS" &&
      aws_ecs_task_definition.service["osrm"].task_role_arn == var.osrm_task_role_arn &&
      length(jsondecode(aws_ecs_task_definition.service["osrm"].container_definitions)[1].secrets) == 0
    )
    error_message = "OSRM reused-role/nonroot/init/secret boundary changed."
  }
  assert {
    condition = (
      contains(jsondecode(aws_ecs_task_definition.service["gateway"].container_definitions)[0].environment, { name = "SHOPSTOPS_BACKEND_URL", value = "http://api.shopstops.internal:8080" }) &&
      contains(jsondecode(aws_ecs_task_definition.service["gateway"].container_definitions)[0].environment, { name = "SHOPSTOPS_OIDC_TRUSTED_CLAIMS", value = "false" }) &&
      length(jsondecode(aws_ecs_task_definition.service["api"].container_definitions)[0].secrets) == 2 &&
      length(jsondecode(aws_ecs_task_definition.service["gateway"].container_definitions)[0].secrets) == 1
    )
    error_message = "Exact gateway environment, default privilege, or secret injection changed."
  }
}

run "optional_waf_disabled" {
  command = plan
  variables { enable_waf = false }
  assert {
    condition     = length(aws_wafv2_web_acl.rate) == 0 && length(aws_wafv2_web_acl_association.rate) == 0
    error_message = "WAF disabling must be explicit and omit both resources."
  }
}

run "reject_certificate_account" {
  command = plan
  variables { certificate_arn = "arn:aws:acm:us-east-1:999999999999:certificate/12345678-1234-1234-1234-123456789abc" }
  expect_failures = [var.certificate_arn]
}

run "reject_secret_region" {
  command = plan
  variables { database_url_secret_arn = "arn:aws:secretsmanager:eu-west-1:246813579024:secret:runtime/database-AbCd12" }
  expect_failures = [var.database_url_secret_arn]
}

run "reject_public_issuer_http" {
  command = plan
  variables { oidc_issuer = "http://identity.shopstops.com" }
  expect_failures = [var.oidc_issuer]
}

run "reject_placeholder_issuer" {
  command = plan
  variables { oidc_issuer = "https://issuer.example.com" }
  expect_failures = [var.oidc_issuer]
}

run "reject_shared_subnets" {
  command = plan
  variables { public_subnet_ids = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef3"] }
  expect_failures = [var.public_subnet_ids]
}

run "reject_dataset_traversal" {
  command = plan
  variables { osrm_dataset_version = "../escape" }
  expect_failures = [var.osrm_dataset_version]
}

run "reject_role_account" {
  command = plan
  variables { osrm_task_role_arn = "arn:aws:iam::999999999999:role/unapproved" }
  expect_failures = [var.osrm_task_role_arn]
}

run "reject_tagged_image" {
  command = plan
  variables {
    images = {
      api     = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/api:latest"
      gateway = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/gateway:latest"
      admin   = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/admin:latest"
      osrm    = "246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/osrm:latest"
    }
  }
  expect_failures = [var.images]
}