# All Lambda functions and ECR repositories.
# Cloud-only (to-www, from-github): always active.
# Fallback (from-youtube, from-discogs, to-spotify, manage-playlists): for k3s failover.

data "aws_caller_identity" "current" {}

locals {
  aws_account_id = data.aws_caller_identity.current.account_id
  aws_region     = "eu-west-1"

  # Cloud-only Lambda functions (always on AWS)
  cloud_lambdas = {
    to-www      = { memory_size = 128, timeout = 35 }
    from-github = { memory_size = 128, timeout = 3 }
  }

  # Fallback Lambda functions (k3s primary, Lambda failover)
  fallback_lambdas = {
    from-youtube     = { memory_size = 128, timeout = 240 }
    from-discogs     = { memory_size = 128, timeout = 240 }
    to-spotify       = { memory_size = 128, timeout = 240 }
    manage-playlists = { memory_size = 512, timeout = 300 }
  }

  all_lambdas = merge(local.cloud_lambdas, local.fallback_lambdas)
}

# --- ECR Repositories (all functions) ---

resource "aws_ecr_repository" "lambda" {
  for_each             = local.all_lambdas
  name                 = each.key
  image_tag_mutability = "MUTABLE"
  force_delete         = false

  image_scanning_configuration {
    scan_on_push = true
  }
}

# --- IAM role (shared by all mirror.fm Lambda functions) ---

data "aws_iam_role" "lambda_role" {
  name = "mirror-fm_lambda_function"
}

# --- Resolve latest ECR image digests ---

data "aws_ecr_image" "latest" {
  for_each        = local.all_lambdas
  repository_name = each.key
  image_tag       = "latest"

  depends_on = [aws_ecr_repository.lambda]
}

# --- Cloud Lambda functions ---

resource "aws_lambda_function" "cloud" {
  for_each      = local.cloud_lambdas
  function_name = "mirror-fm_${each.key}"
  role          = data.aws_iam_role.lambda_role.arn
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.lambda[each.key].repository_url}@${data.aws_ecr_image.latest[each.key].image_digest}"
  architectures = ["arm64"]
  memory_size   = each.value.memory_size
  timeout       = each.value.timeout

  environment {
    variables = local.db_env
  }
}

# --- Fallback Lambda functions ---

data "aws_secretsmanager_secret_version" "function_secrets" {
  for_each  = local.secret_names
  secret_id = aws_secretsmanager_secret.function_secrets[each.key].id
}

locals {
  # Merge DB creds (from SSM) with function-specific vars (from Secrets Manager)
  # Filter out AWS_* and DB_* keys from Secrets Manager (DB comes from SSM)
  fallback_env_vars = {
    for name in local.secret_names : name => merge(
      local.db_env,
      { for k, v in jsondecode(data.aws_secretsmanager_secret_version.function_secrets[name].secret_string) :
        k => v if !startswith(k, "AWS_") && !startswith(k, "DB_") && k != "SQS_QUEUE_URL" && k != "SQS_TO_SPOTIFY_URL"
      }
    )
  }
}

resource "aws_lambda_function" "fallback" {
  for_each      = local.fallback_lambdas
  function_name = "mirror-fm_${each.key}"
  role          = data.aws_iam_role.lambda_role.arn
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.lambda[each.key].repository_url}@${data.aws_ecr_image.latest[each.key].image_digest}"
  architectures = ["arm64"]
  memory_size   = each.value.memory_size
  timeout       = each.value.timeout

  environment {
    variables = local.fallback_env_vars[each.key]
  }
}

# --- API Gateway → Lambda permissions ---

resource "aws_lambda_permission" "api_gateway_to_www" {
  statement_id  = "AllowAPIGateway"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.cloud["to-www"].function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_api_gateway_rest_api.cloud_api["to-www"].execution_arn}/*/*/*"
}

resource "aws_lambda_permission" "api_gateway_from_github" {
  statement_id  = "AllowAPIGateway"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.cloud["from-github"].function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_api_gateway_rest_api.cloud_api["from-github"].execution_arn}/*/*/*"
}

# --- API Gateway REST APIs ---

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
