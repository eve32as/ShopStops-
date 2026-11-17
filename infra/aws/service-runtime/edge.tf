resource "aws_lb" "public" {
  name                       = var.name
  internal                   = false
  load_balancer_type         = "application"
  ip_address_type            = "ipv4"
  security_groups            = [aws_security_group.service["alb"].id]
  subnets                    = var.public_subnet_ids
  drop_invalid_header_fields = true
  enable_deletion_protection = true
}

resource "aws_lb_target_group" "public" {
  for_each             = toset(["gateway", "admin"])
  name_prefix          = each.key == "gateway" ? "ss-gw-" : "ss-ad-"
  port                 = 8080
  protocol             = "HTTP"
  target_type          = "ip"
  vpc_id               = var.vpc_id
  deregistration_delay = 30
  health_check {
    enabled             = true
    path                = "/healthz"
    matcher             = "200"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.public.arn
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
  load_balancer_arn                                            = aws_lb.public.arn
  port                                                         = 443
  protocol                                                     = "HTTPS"
  ssl_policy                                                   = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn                                              = var.certificate_arn
  routing_http_response_strict_transport_security_header_value = "max-age=31536000"
  routing_http_response_x_content_type_options_header_value    = "nosniff"
  routing_http_response_server_enabled                         = false
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.public["admin"].arn
  }
}

resource "aws_lb_listener_rule" "gateway" {
  listener_arn = aws_lb_listener.https.arn
  priority     = 10
  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.public["gateway"].arn
  }
  condition {
    # /v10 and /webhooks/stripe/extra do not match. Gateway enforces exact
    # operation/method policy on every forwarded request; ALB is not auth.
    path_pattern {
      values = ["/v1", "/v1/*", "/webhooks/stripe"]
    }
  }
}

resource "aws_wafv2_web_acl" "rate" {
  count = var.enable_waf ? 1 : 0
  name  = "${var.name}-rate"
  scope = "REGIONAL"
  default_action {
    allow {}
  }
  rule {
    name     = "source-ip-rate"
    priority = 1
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit                 = var.waf_requests_per_five_minutes
        aggregate_key_type    = "IP"
        evaluation_window_sec = 300
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.name}-rate"
      sampled_requests_enabled   = false
    }
  }
  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${var.name}-waf"
    sampled_requests_enabled   = false
  }
}

resource "aws_wafv2_web_acl_association" "rate" {
  count        = var.enable_waf ? 1 : 0
  resource_arn = aws_lb.public.arn
  web_acl_arn  = aws_wafv2_web_acl.rate[0].arn
}