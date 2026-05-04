# GitHub webhook ingestion (data repo CSV submissions).
#
# GitHub webhook → API Gateway HTTP API → SQS (direct integration) → Lambda consumer.
#
# No Lambda in the request path: API Gateway translates each POST into an
# SQS SendMessage and returns 200 to GitHub immediately. The from-github
# Lambda consumes from SQS — failures are retried by SQS (5x → DLQ), so
# Lambda cold-start transients no longer drop CSV rows. GitHub does not
# retry 5xx, so retry MUST happen behind a fast-acknowledging endpoint.
#
# Replaces: legacy REST API + container Lambda (cold-start failures), and
# the brief Lambda Function URL detour (account-level public-access block
# returns 403 for all *.lambda-url.* endpoints; works in Console only).

# --- S3 bucket for Lambda zip artifacts ---

resource "aws_s3_bucket" "lambda_artifacts" {
  bucket = "mirrorfm-lambda-artifacts-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_versioning" "lambda_artifacts" {
  bucket = aws_s3_bucket.lambda_artifacts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "lambda_artifacts" {
  bucket                  = aws_s3_bucket.lambda_artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Placeholder zip so the Lambda can be created on first apply.
# CI replaces this with the real build; we ignore_changes so subsequent
# applies don't overwrite the CI-uploaded artifact.
data "archive_file" "from_github_placeholder" {
  type        = "zip"
  output_path = "${path.module}/.from-github-placeholder.zip"
  source {
    content  = "placeholder"
    filename = "bootstrap"
  }
}

resource "aws_s3_object" "from_github_zip" {
  bucket = aws_s3_bucket.lambda_artifacts.id
  key    = "from-github/lambda.zip"
  source = data.archive_file.from_github_placeholder.output_path
  etag   = data.archive_file.from_github_placeholder.output_md5

  lifecycle {
    ignore_changes = [source, etag]
  }
}

# Always reads the latest version (bumped by CI uploads).
data "aws_s3_object" "from_github_zip_latest" {
  bucket     = aws_s3_bucket.lambda_artifacts.id
  key        = aws_s3_object.from_github_zip.key
  depends_on = [aws_s3_object.from_github_zip]
}

# --- SQS queue + DLQ for inbound webhook events ---

resource "aws_sqs_queue" "github_webhook_dlq" {
  name                      = "mirrorfm-from-github-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "github_webhook" {
  name                       = "mirrorfm-from-github"
  visibility_timeout_seconds = 60 # > Lambda timeout (10s) with margin
  message_retention_seconds  = 1209600
  receive_wait_time_seconds  = 20

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.github_webhook_dlq.arn
    maxReceiveCount     = 5
  })
}

# Allow API Gateway service to send to this queue.
resource "aws_sqs_queue_policy" "github_webhook_apigw" {
  queue_url = aws_sqs_queue.github_webhook.url
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "apigateway.amazonaws.com" }
      Action    = "sqs:SendMessage"
      Resource  = aws_sqs_queue.github_webhook.arn
      Condition = {
        ArnEquals = {
          "aws:SourceArn" = aws_apigatewayv2_api.github_webhook.execution_arn
        }
      }
    }]
  })
}

# --- Webhook secret (kept for future HMAC verification via Lambda authorizer) ---
# HTTP API → SQS direct integration cannot forward the X-Hub-Signature-256
# header into the SQS message, so HMAC verification at ingress is not done
# today. This secret is kept (pre-populated by the operator) so we can later
# attach a Lambda authorizer to the API GW route without re-rolling secrets.
resource "aws_ssm_parameter" "github_webhook_secret" {
  name  = "/mirrorfm/github/webhook-secret"
  type  = "String"
  value = "REPLACE_ME_AFTER_APPLY"

  lifecycle {
    ignore_changes = [value]
  }
}

# --- DB credentials (consumer Lambda reads them to insert into yt_channels) ---

data "aws_ssm_parameter" "from_github_db" {
  for_each        = toset(["db/host", "db/username", "db/password", "db/name"])
  name            = "/mirrorfm/${each.key}"
  with_decryption = true
}

# --- Lambda function (zip, consumer-only — no longer in request path) ---

resource "aws_lambda_function" "from_github" {
  function_name = "mirror-fm_from-github"
  role          = data.aws_iam_role.lambda_role.arn
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]
  memory_size   = 256
  timeout       = 10

  s3_bucket         = aws_s3_bucket.lambda_artifacts.id
  s3_key            = aws_s3_object.from_github_zip.key
  s3_object_version = data.aws_s3_object.from_github_zip_latest.version_id

  environment {
    variables = {
      DB_HOST     = data.aws_ssm_parameter.from_github_db["db/host"].value
      DB_USERNAME = data.aws_ssm_parameter.from_github_db["db/username"].value
      DB_PASSWORD = data.aws_ssm_parameter.from_github_db["db/password"].value
      DB_NAME     = data.aws_ssm_parameter.from_github_db["db/name"].value
    }
  }
}

# --- API Gateway HTTP API → SQS direct integration (no Lambda in request path) ---

resource "aws_apigatewayv2_api" "github_webhook" {
  name          = "mirrorfm-github-webhook"
  protocol_type = "HTTP"
  description   = "GitHub webhook → SQS direct ingest. POST / forwarded to mirrorfm-from-github queue."
}

# IAM role API Gateway assumes to call sqs:SendMessage.
resource "aws_iam_role" "apigw_to_sqs" {
  name = "mirrorfm-apigw-from-github-webhook"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "apigateway.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "apigw_to_sqs" {
  name = "send-to-github-webhook-queue"
  role = aws_iam_role.apigw_to_sqs.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "sqs:SendMessage"
      Resource = aws_sqs_queue.github_webhook.arn
    }]
  })
}

resource "aws_apigatewayv2_integration" "sqs" {
  api_id                 = aws_apigatewayv2_api.github_webhook.id
  integration_type       = "AWS_PROXY"
  integration_subtype    = "SQS-SendMessage"
  credentials_arn        = aws_iam_role.apigw_to_sqs.arn
  payload_format_version = "1.0"

  request_parameters = {
    QueueUrl    = aws_sqs_queue.github_webhook.url
    MessageBody = "$request.body"
  }
}

resource "aws_apigatewayv2_route" "post" {
  api_id    = aws_apigatewayv2_api.github_webhook.id
  route_key = "POST /"
  target    = "integrations/${aws_apigatewayv2_integration.sqs.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.github_webhook.id
  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_burst_limit = 50
    throttling_rate_limit  = 25
  }

  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.github_webhook_apigw.arn
    format = jsonencode({
      requestId      = "$context.requestId"
      ip             = "$context.identity.sourceIp"
      requestTime    = "$context.requestTime"
      httpMethod     = "$context.httpMethod"
      routeKey       = "$context.routeKey"
      status         = "$context.status"
      protocol       = "$context.protocol"
      responseLength = "$context.responseLength"
      integrationErr = "$context.integrationErrorMessage"
    })
  }
}

resource "aws_cloudwatch_log_group" "github_webhook_apigw" {
  name              = "/aws/apigateway/mirrorfm-github-webhook"
  retention_in_days = 14
}

# --- SQS → Lambda event source mapping (consumer) ---

resource "aws_lambda_event_source_mapping" "github_webhook" {
  event_source_arn = aws_sqs_queue.github_webhook.arn
  function_name    = aws_lambda_function.from_github.function_name
  batch_size       = 1
  enabled          = true

  depends_on = [aws_iam_role_policy.lambda_sqs]
}

# --- Outputs ---

output "github_webhook_url" {
  description = "Set this as the GitHub webhook Payload URL in mirrorfm/data settings, replacing the legacy API GW endpoint. Marked sensitive so future TF apply logs (public, since the repo is OSS) don't republish the endpoint — anyone with the URL can POST messages to the SQS ingest, and we don't HMAC-verify at ingress."
  value       = "${aws_apigatewayv2_api.github_webhook.api_endpoint}/"
  sensitive   = true
}
