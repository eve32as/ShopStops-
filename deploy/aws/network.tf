locals {
  name        = "${var.project_name}-${var.environment}"
  az_index    = { for index, az in var.availability_zones : az => index }
  subnet_bits = 8
}

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
}

resource "aws_subnet" "public" {
  for_each = local.az_index

  vpc_id                  = aws_vpc.main.id
  availability_zone       = each.key
  cidr_block              = cidrsubnet(var.vpc_cidr, local.subnet_bits, each.value * 3)
  map_public_ip_on_launch = true
}

resource "aws_subnet" "application" {
  for_each = local.az_index

  vpc_id            = aws_vpc.main.id
  availability_zone = each.key
  cidr_block        = cidrsubnet(var.vpc_cidr, local.subnet_bits, each.value * 3 + 1)
}

resource "aws_subnet" "database" {
  for_each = local.az_index

  vpc_id            = aws_vpc.main.id
  availability_zone = each.key
  cidr_block        = cidrsubnet(var.vpc_cidr, local.subnet_bits, each.value * 3 + 2)
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.main.id
}

resource "aws_route_table_association" "public" {
  for_each = aws_subnet.public

  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

resource "aws_eip" "nat" {
  for_each = local.az_index

  domain = "vpc"
}

resource "aws_nat_gateway" "main" {
  for_each = local.az_index

  allocation_id = aws_eip.nat[each.key].id
  subnet_id     = aws_subnet.public[each.key].id
  depends_on    = [aws_internet_gateway.main]
}

resource "aws_route_table" "application" {
  for_each = local.az_index

  vpc_id = aws_vpc.main.id
}

resource "aws_route" "application_nat" {
  for_each = local.az_index

  route_table_id         = aws_route_table.application[each.key].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.main[each.key].id
}

resource "aws_route_table_association" "application" {
  for_each = aws_subnet.application

  subnet_id      = each.value.id
  route_table_id = aws_route_table.application[each.key].id
}

resource "aws_route_table" "database" {
  vpc_id = aws_vpc.main.id
}

resource "aws_route_table_association" "database" {
  for_each = aws_subnet.database

  subnet_id      = each.value.id
  route_table_id = aws_route_table.database.id
}

resource "aws_security_group" "load_balancer" {
  name        = "${local.name}-alb"
  description = "Public HTTPS ingress to the identity gateway only"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "gateway" {
  name        = "${local.name}-gateway"
  description = "Gateway ingress from the load balancer"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "api" {
  name        = "${local.name}-api"
  description = "API ingress from the identity gateway"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "database" {
  name        = "${local.name}-database"
  description = "PostgreSQL ingress from the API only"
  vpc_id      = aws_vpc.main.id
}

resource "aws_vpc_security_group_ingress_rule" "alb_https" {
  security_group_id = aws_security_group.load_balancer.id
  description       = "HTTPS from public clients"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  ip_protocol       = "tcp"
  to_port           = 443
}

resource "aws_vpc_security_group_ingress_rule" "alb_http" {
  security_group_id = aws_security_group.load_balancer.id
  description       = "HTTP redirect to HTTPS"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 80
  ip_protocol       = "tcp"
  to_port           = 80
}

resource "aws_vpc_security_group_egress_rule" "alb_to_gateway" {
  security_group_id            = aws_security_group.load_balancer.id
  description                  = "Forward requests only to the identity gateway"
  referenced_security_group_id = aws_security_group.gateway.id
  from_port                    = 8081
  ip_protocol                  = "tcp"
  to_port                      = 8081
}

resource "aws_vpc_security_group_ingress_rule" "gateway_from_alb" {
  security_group_id            = aws_security_group.gateway.id
  description                  = "Requests from the public load balancer"
  referenced_security_group_id = aws_security_group.load_balancer.id
  from_port                    = 8081
  ip_protocol                  = "tcp"
  to_port                      = 8081
}

resource "aws_vpc_security_group_egress_rule" "gateway_to_api" {
  security_group_id            = aws_security_group.gateway.id
  description                  = "Forward authenticated requests to the API"
  referenced_security_group_id = aws_security_group.api.id
  from_port                    = 8080
  ip_protocol                  = "tcp"
  to_port                      = 8080
}

resource "aws_vpc_security_group_egress_rule" "gateway_https" {
  security_group_id = aws_security_group.gateway.id
  description       = "HTTPS to OIDC discovery and JWKS endpoints"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  ip_protocol       = "tcp"
  to_port           = 443
}

resource "aws_vpc_security_group_ingress_rule" "api_from_gateway" {
  security_group_id            = aws_security_group.api.id
  description                  = "Requests from the identity gateway only"
  referenced_security_group_id = aws_security_group.gateway.id
  from_port                    = 8080
  ip_protocol                  = "tcp"
  to_port                      = 8080
}

resource "aws_vpc_security_group_egress_rule" "api_to_database" {
  security_group_id            = aws_security_group.api.id
  description                  = "PostgreSQL traffic to the database"
  referenced_security_group_id = aws_security_group.database.id
  from_port                    = 5432
  ip_protocol                  = "tcp"
  to_port                      = 5432
}

resource "aws_vpc_security_group_egress_rule" "api_https" {
  security_group_id = aws_security_group.api.id
  description       = "HTTPS for Stripe API operations"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  ip_protocol       = "tcp"
  to_port           = 443
}

resource "aws_vpc_security_group_ingress_rule" "database_from_api" {
  security_group_id            = aws_security_group.database.id
  description                  = "PostgreSQL from the API only"
  referenced_security_group_id = aws_security_group.api.id
  from_port                    = 5432
  ip_protocol                  = "tcp"
  to_port                      = 5432
}
