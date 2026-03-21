# Cloud Lambda functions (to-www, from-github).
# These stay on AWS — not migrated to k3s.
# Imported from existing resources.

locals {
  aws_account_id = "705440408593"
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
  name = each.value
}
