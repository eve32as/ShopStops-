data "aws_route53_zone" "gateway" {
  zone_id = var.route53_zone_id
}

resource "aws_route53_record" "gateway" {
  zone_id = data.aws_route53_zone.gateway.zone_id
  name    = var.gateway_hostname
  type    = "A"

  alias {
    name                   = aws_lb.gateway.dns_name
    zone_id                = aws_lb.gateway.zone_id
    evaluate_target_health = true
  }

  lifecycle {
    precondition {
      condition = (
        !data.aws_route53_zone.gateway.private_zone &&
        (
          var.gateway_hostname == trimsuffix(data.aws_route53_zone.gateway.name, ".") ||
          endswith(
            var.gateway_hostname,
            ".${trimsuffix(data.aws_route53_zone.gateway.name, ".")}"
          )
        )
      )
      error_message = "route53_zone_id must identify a public hosted zone that contains gateway_hostname."
    }
  }
}
