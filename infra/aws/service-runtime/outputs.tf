output "public_urls" {
  description = "Require manual domain DNS and certificate verification; not live/ready declarations."
  value = {
    admin   = "https://${var.domain}/"
    gateway = "https://${var.domain}/v1"
    health  = "https://${var.domain}/v1/healthz"
    webhook = "https://${var.domain}/webhooks/stripe"
  }
}

output "alb_dns_name" {
  description = "Manually point the approved domain to this ALB; do not use this hostname as an HTTPS certificate bypass."
  value       = aws_lb.public.dns_name
}

output "alb_zone_id" {
  value = aws_lb.public.zone_id
}

output "api_security_group_id" {
  description = "Existing RDS owner must separately review ingress from this SG on 5432; this root never changes RDS."
  value       = aws_security_group.service["api"].id
}

output "cluster_name" {
  value = aws_ecs_cluster.runtime.name
}