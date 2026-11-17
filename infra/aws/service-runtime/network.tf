resource "aws_security_group" "service" {
  for_each    = toset(["alb", "api", "gateway", "admin", "osrm"])
  name_prefix = "${var.name}-${each.key}-"
  description = "ShopStops ${each.key}; explicit rules only"
  vpc_id      = var.vpc_id
  # No inline rules: AWS's default allow-all egress is removed.
}

resource "aws_vpc_security_group_ingress_rule" "public" {
  for_each          = toset(["80", "443"])
  security_group_id = aws_security_group.service["alb"].id
  ip_protocol       = "tcp"
  from_port         = tonumber(each.key)
  to_port           = tonumber(each.key)
  cidr_ipv4         = "0.0.0.0/0"
}

locals {
  connections = {
    alb_gateway = { source = "alb", target = "gateway", port = 8080 }
    alb_admin   = { source = "alb", target = "admin", port = 8080 }
    gateway_api = { source = "gateway", target = "api", port = 8080 }
    api_osrm    = { source = "api", target = "osrm", port = 5001 }
  }
  aws_egress = { for pair in setproduct(["api", "admin", "osrm"], var.aws_https_egress_cidrs) : "${pair[0]}-${pair[1]}" => { service = pair[0], cidr = pair[1] } }
}

resource "aws_vpc_security_group_ingress_rule" "service" {
  for_each                     = local.connections
  security_group_id            = aws_security_group.service[each.value.target].id
  referenced_security_group_id = aws_security_group.service[each.value.source].id
  ip_protocol                  = "tcp"
  from_port                    = each.value.port
  to_port                      = each.value.port
}

resource "aws_vpc_security_group_egress_rule" "service" {
  for_each                     = local.connections
  security_group_id            = aws_security_group.service[each.value.source].id
  referenced_security_group_id = aws_security_group.service[each.value.target].id
  ip_protocol                  = "tcp"
  from_port                    = each.value.port
  to_port                      = each.value.port
}

resource "aws_vpc_security_group_egress_rule" "database" {
  security_group_id            = aws_security_group.service["api"].id
  referenced_security_group_id = var.rds_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}

resource "aws_vpc_security_group_egress_rule" "aws_https" {
  for_each          = local.aws_egress
  security_group_id = aws_security_group.service[each.value.service].id
  cidr_ipv4         = each.value.cidr
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_vpc_security_group_egress_rule" "gateway_https" {
  security_group_id = aws_security_group.service["gateway"].id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  description       = "Approved NAT path for public OIDC discovery/JWKS and AWS task startup"
}

resource "aws_service_discovery_private_dns_namespace" "internal" {
  name = "shopstops.internal"
  vpc  = var.vpc_id
}

resource "aws_service_discovery_service" "private" {
  for_each = toset(["api", "osrm"])
  name     = each.key
  dns_config {
    namespace_id   = aws_service_discovery_private_dns_namespace.internal.id
    routing_policy = "MULTIVALUE"
    dns_records {
      ttl  = 10
      type = "A"
    }
  }
  health_check_custom_config {}
}