# Cloud Lambda functions (to-www, from-github).
# These stay on AWS — not migrated to k3s.
# Imported from existing resources.

data "aws_caller_identity" "current" {}

locals {
  aws_account_id = data.aws_caller_identity.current.account_id
  aws_region     = "eu-west-1"

  cloud_lambdas = {
    to-www = {
      memory_size = 128
      timeout     = 35
    }
    from-github = {
      memory_size = 128
      timeout     = 3
    }
  }
}

# ECR repositories
resource "aws_ecr_repository" "cloud_lambda" {
  for_each             = local.cloud_lambdas
  name                 = each.key
  image_tag_mutability = "MUTABLE"
  force_delete         = false

  image_scanning_configuration {
    scan_on_push = true
  }
}

# IAM role (shared by all mirror.fm Lambda functions)
data "aws_iam_role" "lambda_role" {
  name = "mirror-fm_lambda_function"
}

# Lambda functions
resource "aws_lambda_function" "cloud_lambda" {
  for_each      = local.cloud_lambdas
  function_name = "mirror-fm_${each.key}"
  role          = data.aws_iam_role.lambda_role.arn
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.cloud_lambda[each.key].repository_url}:latest"
  architectures = ["arm64"]
  memory_size   = each.value.memory_size
  timeout       = each.value.timeout

  environment {
    variables = {}
  }

  lifecycle {
    ignore_changes = [image_uri, environment]
  }
}

# API Gateway REST APIs
resource "aws_api_gateway_rest_api" "cloud_api" {
  for_each = {
    to-www      = "mirrorfm-to-www"
    from-github = "mirrorfm-from-github"
  }
  name                         = each.value
  disable_execute_api_endpoint = each.key == "to-www" ? true : false
}

# Redeployment (needed for disable_execute_api_endpoint to take effect)
resource "aws_api_gateway_deployment" "to_www" {
  rest_api_id = aws_api_gateway_rest_api.cloud_api["to-www"].id

  lifecycle {
    create_before_destroy = true
  }
}

# API Gateway stage with throttling (to-www only)
resource "aws_api_gateway_stage" "to_www_api" {
  rest_api_id   = aws_api_gateway_rest_api.cloud_api["to-www"].id
  stage_name    = "api"
  deployment_id = aws_api_gateway_deployment.to_www.id
}

resource "aws_api_gateway_method_settings" "to_www_throttle" {
  rest_api_id = aws_api_gateway_rest_api.cloud_api["to-www"].id
  stage_name  = aws_api_gateway_stage.to_www_api.stage_name
  method_path = "*/*"

  settings {
    throttling_rate_limit  = 10
    throttling_burst_limit = 20
  }
}

# --- Custom domain: api.mirror.fm ---

# Regional ACM cert (API Gateway regional endpoints need cert in same region)
resource "aws_acm_certificate" "api" {
  domain_name       = "api.mirror.fm"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "api_cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.api.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }

  allow_overwrite = true
  name            = each.value.name
  records         = [each.value.record]
  ttl             = 60
  type            = each.value.type
  zone_id         = aws_route53_zone.mirror_fm.zone_id
}

resource "aws_acm_certificate_validation" "api" {
  certificate_arn         = aws_acm_certificate.api.arn
  validation_record_fqdns = [for record in aws_route53_record.api_cert_validation : record.fqdn]
}

# Custom domain name (regional)
resource "aws_api_gateway_domain_name" "api" {
  domain_name              = "api.mirror.fm"
  regional_certificate_arn = aws_acm_certificate_validation.api.certificate_arn

  endpoint_configuration {
    types = ["REGIONAL"]
  }
}

# Map api.mirror.fm/ → to-www API stage "api"
resource "aws_api_gateway_base_path_mapping" "api" {
  api_id      = aws_api_gateway_rest_api.cloud_api["to-www"].id
  stage_name  = aws_api_gateway_stage.to_www_api.stage_name
  domain_name = aws_api_gateway_domain_name.api.domain_name
}

# DNS record
resource "aws_route53_record" "api" {
  zone_id = aws_route53_zone.mirror_fm.zone_id
  name    = "api.mirror.fm"
  type    = "A"

  alias {
    name                   = aws_api_gateway_domain_name.api.regional_domain_name
    zone_id                = aws_api_gateway_domain_name.api.regional_zone_id
    evaluate_target_health = false
  }
}

